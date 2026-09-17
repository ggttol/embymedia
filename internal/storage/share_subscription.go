package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/embymedia/embymedia/internal/domain"
)

func (db *DB) CreateShareSubscription(ctx context.Context, sub domain.ShareSubscription) error {
	_, err := db.db.ExecContext(ctx, `
		INSERT INTO share_subscriptions (id, name, provider, url, password, target_cid, active, last_cursor_time, last_error, last_sync_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sub.ID, sub.Name, sub.Provider, sub.URL, sub.Password, sub.TargetCID, sub.Active, sub.LastCursorTime.UTC(), sub.LastError, sub.LastSyncAt.UTC(), time.Now().UTC(), time.Now().UTC())
	return err
}

func (db *DB) GetShareSubscription(ctx context.Context, id string) (*domain.ShareSubscription, error) {
	var sub domain.ShareSubscription
	var lastError sql.NullString
	var lastSyncAt sql.NullTime

	err := db.db.QueryRowContext(ctx, `
		SELECT id, name, provider, url, password, target_cid, active, last_cursor_time, last_error, last_sync_at, created_at, updated_at
		FROM share_subscriptions WHERE id = ?`, id).Scan(
		&sub.ID, &sub.Name, &sub.Provider, &sub.URL, &sub.Password, &sub.TargetCID, &sub.Active,
		&sub.LastCursorTime, &lastError, &lastSyncAt, &sub.CreatedAt, &sub.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil // not found
	}
	if err != nil {
		return nil, err
	}
	if lastError.Valid {
		sub.LastError = lastError.String
	}
	if lastSyncAt.Valid {
		sub.LastSyncAt = lastSyncAt.Time
	}
	return &sub, nil
}

func (db *DB) UpdateShareSubscription(ctx context.Context, id, name, provider, url, password, targetCID string, active bool) error {
	_, err := db.db.ExecContext(ctx, `
		UPDATE share_subscriptions
		SET name = ?, provider = ?, url = ?, password = ?, target_cid = ?, active = ?, updated_at = ?
		WHERE id = ?`,
		name, provider, url, password, targetCID, active, time.Now().UTC(), id)
	return err
}

func (db *DB) ListShareSubscriptions(ctx context.Context, activeOnly bool) ([]domain.ShareSubscription, error) {
	query := "SELECT id, name, provider, url, password, target_cid, active, last_cursor_time, last_error, last_sync_at, created_at, updated_at FROM share_subscriptions"
	if activeOnly {
		query += " WHERE active = 1"
	}
	query += " ORDER BY created_at DESC"
	rows, err := db.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []domain.ShareSubscription
	for rows.Next() {
		var sub domain.ShareSubscription
		var lastError sql.NullString
		var lastSyncAt sql.NullTime

		if err := rows.Scan(
			&sub.ID, &sub.Name, &sub.Provider, &sub.URL, &sub.Password, &sub.TargetCID, &sub.Active,
			&sub.LastCursorTime, &lastError, &lastSyncAt, &sub.CreatedAt, &sub.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if lastError.Valid {
			sub.LastError = lastError.String
		}
		if lastSyncAt.Valid {
			sub.LastSyncAt = lastSyncAt.Time
		}
		subs = append(subs, sub)
	}
	return subs, nil
}

func (db *DB) UpdateShareSubscriptionCursor(ctx context.Context, id string, lastCursorTime time.Time) error {
	_, err := db.db.ExecContext(ctx, `
		UPDATE share_subscriptions SET last_cursor_time = ?, last_sync_at = ?, last_error = NULL, updated_at = ? WHERE id = ?`,
		lastCursorTime.UTC(), time.Now().UTC(), time.Now().UTC(), id)
	return err
}

func (db *DB) RecordShareSubscriptionError(ctx context.Context, id string, errMsg string) error {
	_, err := db.db.ExecContext(ctx, `
		UPDATE share_subscriptions SET last_error = ?, last_sync_at = ?, updated_at = ? WHERE id = ?`,
		errMsg, time.Now().UTC(), time.Now().UTC(), id)
	return err
}

func (db *DB) DeleteShareSubscription(ctx context.Context, id string) error {
	_, err := db.db.ExecContext(ctx, "DELETE FROM share_subscriptions WHERE id = ?", id)
	return err
}

func (db *DB) ToggleShareSubscription(ctx context.Context, id string, active bool) error {
	_, err := db.db.ExecContext(ctx, "UPDATE share_subscriptions SET active = ?, updated_at = ? WHERE id = ?", active, time.Now().UTC(), id)
	return err
}
