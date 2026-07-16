package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// EnvVarMeta is the non-secret metadata for a project env var — enough for the
// write-only UI to list keys without ever exposing values.
type EnvVarMeta struct {
	Key       string    `json:"key"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EncEnvVar is a stored, still-encrypted env var (for decryption at deploy time).
type EncEnvVar struct {
	Key      string
	ValueEnc []byte
	Nonce    []byte
}

// UpsertEnvVar inserts or replaces a project's env var ciphertext + nonce.
func UpsertEnvVar(ctx context.Context, q DBTX, projectID, key string, valueEnc, nonce []byte) error {
	_, err := q.Exec(ctx, `
		INSERT INTO env_vars (project_id, key, value_enc, nonce, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (project_id, key)
		DO UPDATE SET value_enc = EXCLUDED.value_enc, nonce = EXCLUDED.nonce, updated_at = now()`,
		projectID, key, valueEnc, nonce)
	if err != nil {
		return fmt.Errorf("upsert env var: %w", err)
	}
	return nil
}

// DeleteEnvVar removes a project's env var. Missing keys are not an error.
func DeleteEnvVar(ctx context.Context, q DBTX, projectID, key string) error {
	_, err := q.Exec(ctx, `DELETE FROM env_vars WHERE project_id = $1 AND key = $2`, projectID, key)
	if err != nil {
		return fmt.Errorf("delete env var: %w", err)
	}
	return nil
}

// ListEnvKeys returns a project's env-var keys (no values), newest-updated last.
func ListEnvKeys(ctx context.Context, q DBTX, projectID string) ([]EnvVarMeta, error) {
	rows, err := q.Query(ctx,
		`SELECT key, updated_at FROM env_vars WHERE project_id = $1 ORDER BY key`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list env keys: %w", err)
	}
	defer rows.Close()

	var out []EnvVarMeta
	for rows.Next() {
		var m EnvVarMeta
		if err := rows.Scan(&m.Key, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetEnvVar returns one env var's ciphertext + nonce, or ErrNotFound.
func GetEnvVar(ctx context.Context, q DBTX, projectID, key string) (valueEnc, nonce []byte, err error) {
	err = q.QueryRow(ctx,
		`SELECT value_enc, nonce FROM env_vars WHERE project_id = $1 AND key = $2`,
		projectID, key).Scan(&valueEnc, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get env var: %w", err)
	}
	return valueEnc, nonce, nil
}

// ListEnvEnc returns all of a project's env vars still encrypted, for the
// orchestrator to decrypt at container-create time.
func ListEnvEnc(ctx context.Context, q DBTX, projectID string) ([]EncEnvVar, error) {
	rows, err := q.Query(ctx,
		`SELECT key, value_enc, nonce FROM env_vars WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list env enc: %w", err)
	}
	defer rows.Close()

	var out []EncEnvVar
	for rows.Next() {
		var e EncEnvVar
		if err := rows.Scan(&e.Key, &e.ValueEnc, &e.Nonce); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
