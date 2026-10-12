package storage

import (
	"context"
	"fmt"
	"time"
)

// Role statuses a user can assign locally (never synced anywhere).
const (
	StatusSaved   = "saved"
	StatusApplied = "applied"
	StatusHidden  = "hidden"
)

// ValidRoleStatus reports whether s is an assignable status.
func ValidRoleStatus(s string) bool {
	return s == StatusSaved || s == StatusApplied || s == StatusHidden
}

// SetRoleStatus records the user's status for a role. An empty status clears it.
func (s *Store) SetRoleStatus(ctx context.Context, externalID, status string) error {
	if externalID == "" {
		return fmt.Errorf("role id is required")
	}
	if status == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM role_status WHERE external_id = ?`, externalID)
		return err
	}
	if !ValidRoleStatus(status) {
		return fmt.Errorf("invalid status %q", status)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO role_status (external_id, status, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(external_id) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at
	`, externalID, status, time.Now().UTC())
	return err
}

// ListRoleStatuses returns every assigned status keyed by role external ID.
func (s *Store) ListRoleStatuses(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT external_id, status FROM role_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}
