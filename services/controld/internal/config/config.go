// Package config loads controld's configuration from the environment, with a
// minimal built-in .env loader so `go run` works in dev without exporting vars.
package config

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved runtime configuration. No package-level state;
// callers inject this everywhere (SPEC.md §18).
type Config struct {
	DatabaseURL   string
	ControldAddr  string   // listen addr, e.g. ":8080"
	ControldPort  int      // parsed from ControldAddr, used in Caddy upstreams
	WebPort       int      // Next.js dev port for Caddy upstream
	HTTPPort      int      // public port Caddy publishes
	CaddyAdmin    string   // e.g. http://127.0.0.1:2019
	CaddyOrigins  []string // admin origins baked into every rendered config
	HostInternal  string   // hostname Caddy uses to reach host processes
	AppsNetwork   string   // docker network app containers join
	AdminToken    string   // bearer token for API/dashboard
	WebhookSecret string   // github webhook HMAC secret
	MasterKeyB64  string   // base64 32-byte AES key
	DockerHost    string   // optional docker endpoint override
	Workers       int
	BuildTimeout  time.Duration
	KeepImages    int
	BuilderKeep   string
	LogLevel      string

	// Queue hardening (SPEC.md §7). Defaults match the spec; overridable via env
	// mainly to speed up tests/demos.
	ReaperInterval time.Duration // how often the reaper sweeps (default 30s)
	JobStaleAfter  time.Duration // running job with older locked_at is stale (default 60s)
	Heartbeat      time.Duration // worker locked_at refresh cadence (default 15s)
	LockRetryDelay time.Duration // requeue delay when a project lock is contended (default 10s)
	CancelPoll     time.Duration // how often a worker checks its cancel flag (default 2s)

	// Reconciliation (SPEC.md §13).
	ReconcileInterval time.Duration // how often the reconciler sweeps (default 30s)

	// GC & disk lifecycle (SPEC.md §14).
	GCInterval   time.Duration // how often the scheduled GC runs (default 24h)
	LogRetention time.Duration // delete log_lines older than this (default 14d)
}

// Load reads deploy/.env (if present and vars are unset) then the process env.
func Load() (Config, error) {
	loadDotEnv(getenv("GANTRY_ENV_FILE", "deploy/.env"))

	c := Config{
		DatabaseURL:   getenv("DATABASE_URL", ""),
		ControldAddr:  getenv("CONTROLD_ADDR", ":8080"),
		HTTPPort:      getenvInt("GANTRY_HTTP_PORT", 80),
		CaddyAdmin:    getenv("CADDY_ADMIN", "http://127.0.0.1:2019"),
		HostInternal:  getenv("GANTRY_HOST_INTERNAL", "host.docker.internal"),
		AppsNetwork:   getenv("GANTRY_APPS_NETWORK", "gantry-apps"),
		AdminToken:    getenv("ADMIN_TOKEN", ""),
		WebhookSecret: getenv("GITHUB_WEBHOOK_SECRET", ""),
		MasterKeyB64:  getenv("GANTRY_MASTER_KEY", ""),
		DockerHost:    getenv("DOCKER_HOST", ""),
		Workers:       getenvInt("GANTRY_WORKERS", 2),
		KeepImages:    getenvInt("GANTRY_KEEP_IMAGES", 3),
		BuilderKeep:   getenv("GANTRY_BUILDER_KEEP_STORAGE", "20GB"),
		LogLevel:      getenv("GANTRY_LOG_LEVEL", "info"),
	}
	c.ControldPort = portFromAddr(c.ControldAddr, 8080)
	c.WebPort = portFromAddr(getenv("WEB_ADDR", ":3000"), 3000)
	c.CaddyOrigins = []string{"127.0.0.1:2019", "localhost:2019", "[::1]:2019", "gantry-caddy:2019"}

	bt, err := time.ParseDuration(getenv("GANTRY_BUILD_TIMEOUT", "15m"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid GANTRY_BUILD_TIMEOUT: %w", err)
	}
	c.BuildTimeout = bt

	c.ReaperInterval = getenvDur("GANTRY_REAPER_INTERVAL", 30*time.Second)
	c.JobStaleAfter = getenvDur("GANTRY_JOB_STALE", 60*time.Second)
	c.Heartbeat = getenvDur("GANTRY_HEARTBEAT", 15*time.Second)
	c.LockRetryDelay = getenvDur("GANTRY_LOCK_RETRY_DELAY", 10*time.Second)
	c.CancelPoll = getenvDur("GANTRY_CANCEL_POLL", 2*time.Second)
	c.ReconcileInterval = getenvDur("GANTRY_RECONCILE_INTERVAL", 30*time.Second)
	c.GCInterval = getenvDur("GANTRY_GC_INTERVAL", 24*time.Hour)
	c.LogRetention = getenvDur("GANTRY_LOG_RETENTION", 14*24*time.Hour)

	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required (set it in deploy/.env)")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// getenvDur parses a Go duration (e.g. "30s", "2m") from the env, or returns def
// if unset or unparseable.
func getenvDur(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}

func getenvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// portFromAddr extracts the numeric port from an addr like ":8080" or "1.2.3.4:8080".
func portFromAddr(addr string, def int) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(portStr)
	if err != nil {
		return def
	}
	return n
}

// loadDotEnv sets any KEY it finds in path that isn't already present in the
// environment. Silently no-ops if the file is missing. Not a real dotenv lib —
// just enough for dev (KEY=VALUE, # comments, optional surrounding quotes).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
