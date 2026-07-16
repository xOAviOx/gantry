//go:build integration

// Integration tests for env-var storage against Postgres. Run with:
//
//	make it     # loads DATABASE_URL from deploy/.env
package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/avishuklacode/gantry/services/controld/internal/secret"
)

func envTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping env store integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func envTestProject(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	slug := fmt.Sprintf("env-it-%d", time.Now().UnixNano())
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (name, slug, repo_url, port) VALUES ($1,$1,'/tmp/x',3000) RETURNING id::text`,
		slug).Scan(&id); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id=$1`, id) })
	return id
}

// Full round-trip: encrypt → upsert → list (no values) → get → decrypt, and the
// stored bytes must not contain the plaintext.
func TestEnvVarStoreRoundTrip(t *testing.T) {
	pool := envTestPool(t)
	ctx := context.Background()
	proj := envTestProject(t, pool)

	key, err := secret.New(base64Key(t))
	if err != nil {
		t.Fatal(err)
	}
	const plaintext = "postgres://user:sup3r-secret@db/app"

	ct, nonce, err := key.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertEnvVar(ctx, pool, proj, "DATABASE_URL", ct, nonce); err != nil {
		t.Fatal(err)
	}

	// List returns metadata only.
	metas, err := ListEnvKeys(ctx, pool, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Key != "DATABASE_URL" {
		t.Fatalf("ListEnvKeys = %+v, want one DATABASE_URL", metas)
	}

	// The raw stored ciphertext must not contain the plaintext.
	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT value_enc FROM env_vars WHERE project_id=$1 AND key='DATABASE_URL'`, proj).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if containsSub(stored, []byte("sup3r-secret")) {
		t.Fatal("plaintext secret found in stored ciphertext")
	}

	// Get + decrypt round-trips.
	gotCT, gotNonce, err := GetEnvVar(ctx, pool, proj, "DATABASE_URL")
	if err != nil {
		t.Fatal(err)
	}
	dec, err := key.Decrypt(gotCT, gotNonce)
	if err != nil {
		t.Fatal(err)
	}
	if dec != plaintext {
		t.Fatalf("decrypted %q, want %q", dec, plaintext)
	}

	// ListEnvEnc feeds the orchestrator; must also decrypt.
	encs, err := ListEnvEnc(ctx, pool, proj)
	if err != nil || len(encs) != 1 {
		t.Fatalf("ListEnvEnc = %+v, err %v", encs, err)
	}

	// Delete removes it; Get then reports ErrNotFound.
	if err := DeleteEnvVar(ctx, pool, proj, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := GetEnvVar(ctx, pool, proj, "DATABASE_URL"); err != ErrNotFound {
		t.Fatalf("after delete, GetEnvVar err = %v, want ErrNotFound", err)
	}
}

func base64Key(t *testing.T) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(make([]byte, 32)) // 32-byte test key
}

func containsSub(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}
