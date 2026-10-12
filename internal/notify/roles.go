package notify

import (
	"fmt"
	"sort"
	"strings"

	"specter/internal/ats"
	"specter/internal/signals"
)

// MessageFromRoles builds an alert from new postings, ranked by global-remote
// status then personal skill match, truncated to max entries (0 = no limit).
func MessageFromRoles(roles []ats.JobPosting, skills []string, max int) Message {
	items := make([]Role, 0, len(roles))
	for _, r := range roles {
		corpus := r.Title + " " + r.Department + " " + r.Location + " " + r.RemotePolicy + " " + strings.Join(r.Keywords, " ")
		score, _ := signals.CalculateSkillMatch(corpus, skills)
		link := r.ApplyURL
		if link == "" {
			link = r.URL
		}
		items = append(items, Role{
			Company:  r.CompanyName,
			Title:    r.Title,
			Location: r.Location,
			URL:      link,
			Score:    score,
			Global:   r.GlobalRemote,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Global != items[j].Global {
			return items[i].Global
		}
		return items[i].Score > items[j].Score
	})

	total := len(items)
	if max > 0 && len(items) > max {
		items = items[:max]
	}
	title := fmt.Sprintf("⚡ Specter: %d new role(s)", total)
	if len(items) < total {
		title += fmt.Sprintf(" (top %d shown)", len(items))
	}
	return Message{Title: title, Roles: items}
}
