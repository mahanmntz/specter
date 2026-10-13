package web

import (
	"net/http"
	"sort"
	"time"

	"github.com/mahanmntz/specter/internal/storage"
)

type stage struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Jobs  []Job  `json:"jobs"`
}

var stageLabels = map[string]string{
	storage.StatusSaved: "Saved", storage.StatusApplied: "Applied", storage.StatusInterview: "Interview",
	storage.StatusOffer: "Offer", storage.StatusRejected: "Rejected",
}

// followUpDue reports whether a follow-up reminder is due today or overdue.
func followUpDue(j Job, now time.Time) bool {
	if j.FollowUpAt.IsZero() || j.Status == "" || j.Status == storage.StatusHidden {
		return false
	}
	endOfToday := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.UTC)
	return !j.FollowUpAt.After(endOfToday)
}

// handlePipeline returns the user's tracked jobs grouped into board columns. Jobs whose posting
// has since been taken down are included (flagged closed) so nothing the user tracks disappears.
func (s *Server) handlePipeline(w http.ResponseWriter, r *http.Request) {
	active, err := s.load(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tracking, err := s.src.ListRoleTracking(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	byID := make(map[string]Job, len(active))
	for _, j := range active {
		byID[j.ID] = j
	}
	var missing []string
	for id, t := range tracking {
		if _, ok := byID[id]; !ok && t.Status != storage.StatusHidden {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		old, err := s.src.ListDirectApplyRoles(r.Context(), storage.DirectApplyFilter{ExternalIDs: missing, IncludeInactive: true})
		if err == nil {
			s.mu.RLock()
			skills := s.skills
			s.mu.RUnlock()
			for _, role := range old {
				byID[role.ID] = toJob(role, skills, tracking[role.ID])
			}
		}
	}

	now := s.now()
	cols := map[string][]Job{}
	due := 0
	for id, t := range tracking {
		j, ok := byID[id]
		if !ok || t.Status == storage.StatusHidden || t.Status == "" {
			continue
		}
		cols[t.Status] = append(cols[t.Status], j)
		if followUpDue(j, now) {
			due++
		}
	}

	stages := make([]stage, 0, len(storage.PipelineStages))
	for _, id := range storage.PipelineStages {
		jobs := cols[id]
		sort.SliceStable(jobs, func(a, b int) bool {
			ja, jb := jobs[a], jobs[b]
			// Reminders first (soonest at the top), then most recently applied/updated.
			if ja.FollowUpAt.IsZero() != jb.FollowUpAt.IsZero() {
				return !ja.FollowUpAt.IsZero()
			}
			if !ja.FollowUpAt.Equal(jb.FollowUpAt) {
				return ja.FollowUpAt.Before(jb.FollowUpAt)
			}
			return ja.AppliedAt.After(jb.AppliedAt)
		})
		if jobs == nil {
			jobs = []Job{}
		}
		stages = append(stages, stage{ID: id, Label: stageLabels[id], Jobs: jobs})
	}
	writeJSON(w, http.StatusOK, map[string]any{"stages": stages, "follow_ups_due": due})
}
