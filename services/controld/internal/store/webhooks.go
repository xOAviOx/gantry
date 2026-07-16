package store

import (
	"context"
	"fmt"
)

// RecordDelivery records a webhook delivery id for idempotency. It returns true
// if this is the first time we've seen the id (fresh), false if it's a replay.
func RecordDelivery(ctx context.Context, q DBTX, deliveryID string) (fresh bool, err error) {
	tag, err := q.Exec(ctx,
		`INSERT INTO webhook_deliveries (delivery_id) VALUES ($1) ON CONFLICT (delivery_id) DO NOTHING`,
		deliveryID)
	if err != nil {
		return false, fmt.Errorf("record delivery: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
