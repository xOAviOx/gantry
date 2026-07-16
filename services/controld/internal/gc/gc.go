// Package gc reclaims disk: it enforces per-project image retention, prunes
// dangling images and the BuildKit cache, purges old log lines, and clears exited
// app containers (SPEC.md §14). It also summarizes disk usage for the dashboard.
package gc

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avishuklacode/gantry/services/controld/internal/config"
	"github.com/avishuklacode/gantry/services/controld/internal/docker"
	"github.com/avishuklacode/gantry/services/controld/internal/store"
)

// ErrBusy is returned when a GC run is requested while one is already in progress.
var ErrBusy = errors.New("gc already in progress")

// Report summarizes what a GC pass reclaimed.
type Report struct {
	ImagesRemoved     int    `json:"images_removed"`
	ContainersRemoved int    `json:"containers_removed"`
	DanglingReclaimed uint64 `json:"dangling_reclaimed"`
	CacheReclaimed    uint64 `json:"cache_reclaimed"`
	LogLinesPurged    int64  `json:"log_lines_purged"`
	DurationMS        int64  `json:"duration_ms"`
}

// Collector runs GC. A mutex ensures the scheduled sweep and an on-demand request
// never overlap.
type Collector struct {
	dc   *docker.Client
	pool *pgxpool.Pool
	cfg  config.Config
	log  *slog.Logger
	mu   sync.Mutex
}

func New(dc *docker.Client, pool *pgxpool.Pool, cfg config.Config, log *slog.Logger) *Collector {
	return &Collector{dc: dc, pool: pool, cfg: cfg, log: log}
}

func managedFilter() filters.Args {
	return filters.NewArgs(filters.Arg("label", docker.LabelManaged+"="+docker.ManagedValue))
}

// Run performs a full GC pass. Returns ErrBusy if one is already running.
func (c *Collector) Run(ctx context.Context) (Report, error) {
	if !c.mu.TryLock() {
		return Report{}, ErrBusy
	}
	defer c.mu.Unlock()

	start := time.Now()
	var rep Report

	// Order matters: drop exited containers first so their images become removable.
	rep.ContainersRemoved = c.removeExitedContainers(ctx)
	rep.ImagesRemoved = c.enforceImageRetention(ctx)
	rep.DanglingReclaimed = c.pruneDangling(ctx)
	rep.CacheReclaimed = c.pruneBuildCache(ctx)
	rep.LogLinesPurged = c.purgeLogs(ctx)

	rep.DurationMS = time.Since(start).Milliseconds()
	c.log.Info("gc complete",
		"images_removed", rep.ImagesRemoved,
		"containers_removed", rep.ContainersRemoved,
		"dangling_reclaimed", rep.DanglingReclaimed,
		"cache_reclaimed", rep.CacheReclaimed,
		"log_lines_purged", rep.LogLinesPurged,
		"dur_ms", rep.DurationMS)
	return rep, nil
}

// RunScheduled runs GC on a fixed interval until ctx is canceled (SPEC.md §14
// "nightly").
func (c *Collector) RunScheduled(ctx context.Context, interval time.Duration) {
	c.log.Info("gc scheduler starting", "interval", interval)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := c.Run(ctx); err != nil && !errors.Is(err, ErrBusy) {
				c.log.Error("scheduled gc failed", "err", err)
			}
		}
	}
}

// removeExitedContainers deletes gantry app-containers (deployment-labeled) that
// are no longer running — leftovers from failed/retired deploys. Infra containers
// have no deployment label and are never touched.
func (c *Collector) removeExitedContainers(ctx context.Context) int {
	list, err := c.dc.ContainerList(ctx, container.ListOptions{All: true, Filters: managedFilter()})
	if err != nil {
		c.log.Error("gc: list containers", "err", err)
		return 0
	}
	removed := 0
	for _, ct := range list {
		if ct.Labels[docker.LabelDeployment] == "" {
			continue
		}
		switch string(ct.State) {
		case "exited", "dead", "created":
			if err := c.dc.ContainerRemove(ctx, ct.ID, container.RemoveOptions{Force: true}); err != nil {
				c.log.Warn("gc: remove container", "id", ct.ID, "err", err)
				continue
			}
			removed++
		}
	}
	return removed
}

// enforceImageRetention removes gantry images whose tag is not in the keep set
// (the last GANTRY_KEEP_IMAGES deployments per project + live). Untagged
// (dangling) images are left to pruneDangling.
func (c *Collector) enforceImageRetention(ctx context.Context) int {
	keep, err := store.ImageTagsToKeep(ctx, c.pool, c.cfg.KeepImages)
	if err != nil {
		c.log.Error("gc: keep set", "err", err)
		return 0
	}
	imgs, err := c.dc.ImageList(ctx, image.ListOptions{Filters: managedFilter()})
	if err != nil {
		c.log.Error("gc: list images", "err", err)
		return 0
	}
	removed := 0
	for _, im := range imgs {
		tags := realTags(im.RepoTags)
		if len(tags) == 0 {
			continue // dangling — handled by pruneDangling
		}
		if anyKept(tags, keep) {
			continue
		}
		if _, err := c.dc.ImageRemove(ctx, im.ID, image.RemoveOptions{Force: true, PruneChildren: true}); err != nil {
			c.log.Warn("gc: remove image", "id", im.ID, "tags", tags, "err", err)
			continue
		}
		c.log.Info("gc removed image", "tags", tags)
		removed++
	}
	return removed
}

func (c *Collector) pruneDangling(ctx context.Context) uint64 {
	f := managedFilter()
	f.Add("dangling", "true")
	rep, err := c.dc.ImagesPrune(ctx, f)
	if err != nil {
		c.log.Warn("gc: prune dangling", "err", err)
		return 0
	}
	return rep.SpaceReclaimed
}

func (c *Collector) pruneBuildCache(ctx context.Context) uint64 {
	keep := ParseBytes(c.cfg.BuilderKeep, 20*1_000_000_000)
	rep, err := c.dc.BuildCachePrune(ctx, build.CachePruneOptions{
		ReservedSpace: keep,
		KeepStorage:   keep, // deprecated alias, set for older daemons
	})
	if err != nil {
		c.log.Warn("gc: prune build cache", "err", err)
		return 0
	}
	if rep == nil {
		return 0
	}
	return rep.SpaceReclaimed
}

func (c *Collector) purgeLogs(ctx context.Context) int64 {
	n, err := store.PurgeOldLogs(ctx, c.pool, c.cfg.LogRetention)
	if err != nil {
		c.log.Warn("gc: purge logs", "err", err)
		return 0
	}
	return n
}

// --- disk usage summary (docker system df) ---

// DiskCategory is one row of the disk widget.
type DiskCategory struct {
	Count       int   `json:"count"`
	Size        int64 `json:"size"`
	Reclaimable int64 `json:"reclaimable"`
}

// DiskReport mirrors `docker system df` for the dashboard widget.
type DiskReport struct {
	Images           DiskCategory `json:"images"`
	Containers       DiskCategory `json:"containers"`
	BuildCache       DiskCategory `json:"build_cache"`
	TotalReclaimable int64        `json:"total_reclaimable"`
}

// Disk summarizes daemon disk usage.
func (c *Collector) Disk(ctx context.Context) (DiskReport, error) {
	du, err := c.dc.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return DiskReport{}, err
	}

	var rep DiskReport

	// Images: total unique on-disk size is LayersSize; reclaimable ≈ the private
	// (non-shared) size of images with no container using them.
	rep.Images.Count = len(du.Images)
	rep.Images.Size = du.LayersSize
	for _, im := range du.Images {
		if im.Containers <= 0 {
			shared := im.SharedSize
			if shared < 0 {
				shared = 0
			}
			priv := im.Size - shared
			if priv > 0 {
				rep.Images.Reclaimable += priv
			}
		}
	}

	// Containers: writable-layer size; a stopped container is fully reclaimable.
	rep.Containers.Count = len(du.Containers)
	for _, ct := range du.Containers {
		rep.Containers.Size += ct.SizeRw
		if string(ct.State) != "running" {
			rep.Containers.Reclaimable += ct.SizeRw
		}
	}

	// Build cache: count non-shared records for size; not-in-use is reclaimable.
	for _, bc := range du.BuildCache {
		if !bc.Shared {
			rep.BuildCache.Size += bc.Size
		}
		rep.BuildCache.Count++
		if !bc.InUse && !bc.Shared {
			rep.BuildCache.Reclaimable += bc.Size
		}
	}

	rep.TotalReclaimable = rep.Images.Reclaimable + rep.Containers.Reclaimable + rep.BuildCache.Reclaimable
	return rep, nil
}

// --- helpers ---

func realTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t != "" && t != "<none>:<none>" {
			out = append(out, t)
		}
	}
	return out
}

func anyKept(tags []string, keep map[string]bool) bool {
	for _, t := range tags {
		if keep[t] {
			return true
		}
	}
	return false
}

// ParseBytes parses sizes like "20GB", "512MiB", "1073741824". Decimal suffixes
// (KB/MB/GB/TB) are powers of 1000; binary (KiB/MiB/GiB/TiB) powers of 1024. On a
// parse failure it returns def.
func ParseBytes(s string, def int64) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	up := strings.ToUpper(s)
	type unit struct {
		suf string
		mul int64
	}
	// Longest suffixes first so "GIB" matches before "GB"/"B".
	units := []unit{
		{"TIB", 1 << 40}, {"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"TB", 1_000_000_000_000}, {"GB", 1_000_000_000}, {"MB", 1_000_000}, {"KB", 1_000},
		{"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(up, u.suf) {
			num := strings.TrimSpace(up[:len(up)-len(u.suf)])
			f, err := strconv.ParseFloat(num, 64)
			if err != nil {
				return def
			}
			return int64(f * float64(u.mul))
		}
	}
	if n, err := strconv.ParseInt(up, 10, 64); err == nil {
		return n
	}
	return def
}
