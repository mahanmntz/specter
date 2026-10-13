package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Stages a user can move a role through, plus "hidden" to dismiss it. All of it stays local.
const (
	StatusSaved     = "saved"
	StatusApplied   = "applied"
	StatusInterview = "interview"
	StatusOffer     = "offer"
	StatusRejected  = "rejected"
	StatusHidden    = "hidden"
)

// PipelineStages are the board columns in order.
var PipelineStages = []string{StatusSaved, StatusApplied, StatusInterview, StatusOffer, StatusRejected}

// DefaultFollowUp is how long after applying a follow-up reminder is suggested.
const DefaultFollowUp = 7 * 24 * time.Hour

// ValidRoleStatus reports whether s is an assignable status.
func ValidRoleStatus(s string) bool {
	switch s {
	case StatusSaved, StatusApplied, StatusInterview, StatusOffer, StatusRejected, StatusHidden:
		return true
	}
	return false
}

// Tracking is everything the user recorded about a role.
type Tracking struct {
	Status     string
	Note       string
	AppliedAt  time.Time
	FollowUpAt time.Time
	UpdatedAt  time.Time
}

// TrackingUpdate changes some fields of a role's tracking; nil fields are left alone.
type TrackingUpdate struct {
	Status *string // "" clears the status (and with it the tracking row, unless a note exists)
	Note   *string
	// FollowUp sets the reminder date; the zero time clears it.
	FollowUp *time.Time
}

// UpdateTracking applies u to the role. Moving a role to "applied" stamps the application date and
// suggests a follow-up a week later; finished stages (offer, rejected) drop the reminder.
func (s *Store) UpdateTracking(ctx context.Context, externalID string, u TrackingUpdate) error {
	if externalID == "" {
		return fmt.Errorf("role id is required")
	}
	if u.Status != nil && *u.Status != "" && !ValidRoleStatus(*u.Status) {
		return fmt.Errorf("invalid status %q", *u.Status)
	}
	if u.Note != nil && len(*u.Note) > 4000 {
		return fmt.Errorf("note is too long")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	cur, exists, err := getTracking(ctx, tx, externalID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if u.Status != nil {
		prev := cur.Status
		cur.Status = *u.Status
		switch cur.Status {
		case StatusApplied:
			if prev != StatusApplied {
				if cur.AppliedAt.IsZero() {
					cur.AppliedAt = now
				}
				if cur.FollowUpAt.IsZero() {
					cur.FollowUpAt = now.Add(DefaultFollowUp)
				}
			}
		case StatusOffer, StatusRejected, StatusHidden, "":
			cur.FollowUpAt = time.Time{}
		}
	}
	if u.Note != nil {
		cur.Note = *u.Note
	}
	if u.FollowUp != nil {
		cur.FollowUpAt = *u.FollowUp
	}

	if cur.Status == "" && cur.Note == "" {
		if exists {
			if _, err := tx.ExecContext(ctx, `DELETE FROM role_status WHERE external_id = ?`, externalID); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	// A role with a note but no stage is kept as "saved" so the note is not orphaned.
	status := cur.Status
	if status == "" {
		status = StatusSaved
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO role_status (external_id, status, updated_at, note, applied_at, follow_up_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(external_id) DO UPDATE SET
			status = excluded.status, updated_at = excluded.updated_at, note = excluded.note,
			applied_at = excluded.applied_at, follow_up_at = excluded.follow_up_at
	`, externalID, status, now, cur.Note, nullTime(cur.AppliedAt), nullTime(cur.FollowUpAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getTracking(ctx context.Context, q queryer, id string) (Tracking, bool, error) {
	var t Tracking
	var applied, follow, updated any
	err := q.QueryRowContext(ctx, `SELECT status, COALESCE(note, ''), applied_at, follow_up_at, updated_at FROM role_status WHERE external_id = ?`, id).
		Scan(&t.Status, &t.Note, &applied, &follow, &updated)
	if err == sql.ErrNoRows {
		return Tracking{}, false, nil
	}
	if err != nil {
		return Tracking{}, false, err
	}
	t.AppliedAt, t.FollowUpAt, t.UpdatedAt = parseSQLiteTime(applied), parseSQLiteTime(follow), parseSQLiteTime(updated)
	return t, true, nil
}

// ListRoleTracking returns every tracked role keyed by external ID.
func (s *Store) ListRoleTracking(ctx context.Context) (map[string]Tracking, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT external_id, status, COALESCE(note, ''), applied_at, follow_up_at, updated_at FROM role_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]Tracking)
	for rows.Next() {
		var id string
		var t Tracking
		var applied, follow, updated any
		if err := rows.Scan(&id, &t.Status, &t.Note, &applied, &follow, &updated); err != nil {
			return nil, err
		}
		t.AppliedAt, t.FollowUpAt, t.UpdatedAt = parseSQLiteTime(applied), parseSQLiteTime(follow), parseSQLiteTime(updated)
		out[id] = t
	}
	return out, rows.Err()
}

// SetRoleStatus records the user's status for a role. An empty status clears it.
func (s *Store) SetRoleStatus(ctx context.Context, externalID, status string) error {
	return s.UpdateTracking(ctx, externalID, TrackingUpdate{Status: &status})
}

// ListRoleStatuses returns every assigned status keyed by role external ID.
func (s *Store) ListRoleStatuses(ctx context.Context) (map[string]string, error) {
	tr, err := s.ListRoleTracking(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(tr))
	for id, t := range tr {
		out[id] = t.Status
	}
	return out, nil
}
