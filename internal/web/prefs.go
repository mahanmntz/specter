package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mahanmntz/specter/internal/signals"
)

const prefsKey = "prefs"

// Prefs are the user's saved search preferences.
type Prefs struct {
	Role          string   `json:"role"`
	Skills        []string `json:"skills"`
	Seniority     []string `json:"seniority"`
	WorldwideOnly bool     `json:"worldwide_only"`
	Feeds         bool     `json:"feeds"` // also pull RemoteOK + Hacker News
}

// normalize validates and cleans p in place.
func (p *Prefs) normalize() error {
	p.Role = strings.ToLower(strings.TrimSpace(p.Role))
	if _, ok := signals.PresetByID(p.Role); !ok {
		return fmt.Errorf("unknown role %q", p.Role)
	}
	p.Skills = cleanStrings(p.Skills, 30, 40)
	p.Seniority = cleanStrings(p.Seniority, 6, 40)
	return nil
}

func cleanStrings(in []string, maxItems, maxLen int) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || len(s) > maxLen || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
		if len(out) == maxItems {
			break
		}
	}
	return out
}

// profile turns the prefs into the matcher profile.
func (p Prefs) profile() (signals.Profile, bool) {
	return signals.ProfileFor(p.Role, p.Skills)
}

func (s *Server) loadPrefs(ctx context.Context) {
	raw, ok, err := s.src.GetSetting(ctx, prefsKey)
	if err != nil || !ok {
		return
	}
	var p Prefs
	if json.Unmarshal([]byte(raw), &p) != nil || p.normalize() != nil {
		return
	}
	s.setPrefs(p, false)
}

// setPrefs activates p (matcher profile + scoring skills) and optionally persists it.
func (s *Server) setPrefs(p Prefs, persist bool) error {
	prof, ok := p.profile()
	if !ok {
		return fmt.Errorf("unknown role %q", p.Role)
	}
	if persist {
		data, _ := json.Marshal(p)
		if err := s.src.SetSetting(context.Background(), prefsKey, string(data)); err != nil {
			return err
		}
	}
	signals.ApplyProfile(prof)
	s.mu.Lock()
	s.prefs = &p
	s.skills = prof.Skills
	s.mu.Unlock()
	return nil
}
