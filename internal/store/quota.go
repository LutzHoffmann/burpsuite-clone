package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const (
	DefaultCaptureLimitBytes int64 = 1 << 30
	MaxCaptureLimitBytes     int64 = 1 << 40
	// CaptureRecordAllowance covers fixed scalar fields; indexes, derived Site Map
	// data, settings, SQLite pages and journals are outside the logical budget.
	CaptureRecordAllowance int64 = 256
)

var ErrCaptureQuotaExceeded = errors.New("capture storage quota exceeded")

type StorageStatus struct {
	Revision       int64 `json:"-"`
	LimitBytes     int64 `json:"limitBytes"`
	UsedBytes      int64 `json:"usedBytes"`
	Paused         bool  `json:"paused"`
	SkippedRecords int64 `json:"skippedRecords"`
}

type QuotaStore interface {
	StorageStatus(context.Context) (StorageStatus, error)
	SetStorageLimit(context.Context, int64) (StorageStatus, error)
}

var _ QuotaStore = (*SQLiteStore)(nil)

const storageStatusQuery = `SELECT limit_bytes, used_bytes, paused, skipped_records, revision FROM capture_quota WHERE project_id = 1`

func (s *SQLiteStore) StorageStatus(ctx context.Context) (StorageStatus, error) {
	var status StorageStatus
	err := s.db.QueryRowContext(ctx, storageStatusQuery).Scan(&status.LimitBytes, &status.UsedBytes, &status.Paused, &status.SkippedRecords, &status.Revision)
	if err != nil {
		return StorageStatus{}, fmt.Errorf("get capture quota: %w", err)
	}
	return status, nil
}

func (s *SQLiteStore) SetStorageLimit(ctx context.Context, limit int64) (StorageStatus, error) {
	if limit <= 0 || limit > MaxCaptureLimitBytes {
		return StorageStatus{}, fmt.Errorf("capture limit must be between 1 and %d bytes", MaxCaptureLimitBytes)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StorageStatus{}, fmt.Errorf("begin capture limit transaction: %w", err)
	}
	defer tx.Rollback()
	// Compare against the old limit in this same write, never an earlier read.
	_, err = tx.ExecContext(ctx, `UPDATE capture_quota SET
 paused = CASE WHEN ? > limit_bytes AND ? > used_bytes THEN 0
               WHEN ? <= used_bytes THEN 1 ELSE paused END,
 limit_bytes = ?, revision = revision + 1 WHERE project_id = 1`, limit, limit, limit, limit)
	if err != nil {
		return StorageStatus{}, fmt.Errorf("set capture limit: %w", err)
	}
	var status StorageStatus
	if err := tx.QueryRowContext(ctx, storageStatusQuery).Scan(&status.LimitBytes, &status.UsedBytes, &status.Paused, &status.SkippedRecords, &status.Revision); err != nil {
		return StorageStatus{}, fmt.Errorf("read updated capture limit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return StorageStatus{}, fmt.Errorf("commit capture limit: %w", err)
	}
	return status, nil
}

// reserveCapture must be the transaction's first statement. SQLite serializes
// this conditional write across connections/processes, avoiding stale snapshots.
// A rejection commits only the latch/counter; successful reservations are committed
// by the caller together with the entire record, or rolled back on any failure.
func reserveCapture(ctx context.Context, tx *sql.Tx, charge int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE capture_quota SET used_bytes = used_bytes + ?
 WHERE project_id = 1 AND paused = 0 AND ? <= limit_bytes - used_bytes`, charge, charge)
	if err != nil {
		return fmt.Errorf("reserve capture quota: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count capture reservation: %w", err)
	}
	if n != 0 {
		return nil
	}
	result, err = tx.ExecContext(ctx, `UPDATE capture_quota SET paused = 1, skipped_records = skipped_records + 1,
 revision = revision + CASE WHEN paused = 0 THEN 1 ELSE 0 END WHERE project_id = 1`)
	if err != nil {
		return fmt.Errorf("pause capture quota: %w", err)
	}
	n, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count capture pause: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("capture quota missing: %w", sql.ErrNoRows)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit capture pause: %w", err)
	}
	return ErrCaptureQuotaExceeded
}

func captureTextBytes(fields ...string) int64 {
	var n int64
	for _, field := range fields {
		n += int64(len(field))
	}
	return n
}
