//go:build integration

package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ImageTagsToKeep must retain each project's keepN most recent deployments plus
// any live one, and drop the rest.
func TestImageTagsToKeep(t *testing.T) {
	pool := envTestPool(t)
	ctx := context.Background()
	proj := envTestProject(t, pool)

	// Insert 5 deployments oldest→newest with distinct image tags; #4 (0-indexed)
	// is the live one, the rest retired.
	base := time.Now().Add(-time.Hour)
	tags := []string{"img1", "img2", "img3", "img4", "img5"}
	for i, tag := range tags {
		status := "retired"
		if i == len(tags)-1 {
			status = "live"
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO deployments (project_id, trigger, status, image_tag, created_at)
			VALUES ($1,'manual',$2,$3,$4)`,
			proj, status, tag, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	keep, err := ImageTagsToKeep(ctx, pool, 3)
	if err != nil {
		t.Fatal(err)
	}
	// Newest 3 are img5(live), img4, img3; img1 and img2 must be gone.
	for _, want := range []string{"img5", "img4", "img3"} {
		if !keep[want] {
			t.Errorf("expected to keep %q", want)
		}
	}
	for _, drop := range []string{"img1", "img2"} {
		if keep[drop] {
			t.Errorf("expected to drop %q", drop)
		}
	}
}

// A live deployment older than the keepN window is still retained.
func TestImageTagsToKeepAlwaysKeepsLive(t *testing.T) {
	pool := envTestPool(t)
	ctx := context.Background()
	proj := envTestProject(t, pool)

	base := time.Now().Add(-time.Hour)
	// Oldest deployment is the live one; then 3 newer retired ones.
	rows := []struct {
		tag    string
		status string
	}{
		{"oldlive", "live"},
		{"newer1", "retired"},
		{"newer2", "retired"},
		{"newer3", "retired"},
	}
	for i, r := range rows {
		if _, err := pool.Exec(ctx, `
			INSERT INTO deployments (project_id, trigger, status, image_tag, created_at)
			VALUES ($1,'manual',$2,$3,$4)`,
			proj, r.status, r.tag, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	keep, err := ImageTagsToKeep(ctx, pool, 2) // window excludes the oldest
	if err != nil {
		t.Fatal(err)
	}
	if !keep["oldlive"] {
		t.Error("live image must be kept even outside the keepN window")
	}
}

func TestPurgeOldLogs(t *testing.T) {
	pool := envTestPool(t)
	ctx := context.Background()
	proj := envTestProject(t, pool)

	var depID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO deployments (project_id, trigger, status) VALUES ($1,'manual','live') RETURNING id::text`,
		proj).Scan(&depID); err != nil {
		t.Fatal(err)
	}
	// One old line (20 days) and one fresh line.
	for i, age := range []time.Duration{20 * 24 * time.Hour, time.Minute} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO log_lines (deployment_id, seq, stream, line, ts) VALUES ($1,$2,'system','x', now()-$3::interval)`,
			depID, i+1, fmt.Sprintf("%d seconds", int(age.Seconds()))); err != nil {
			t.Fatal(err)
		}
	}

	purged, err := PurgeOldLogs(ctx, pool, 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Fatalf("purged %d lines, want 1 (only the 20-day-old one)", purged)
	}
}
