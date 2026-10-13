package seeds

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

const (
	userSeedsKey     = "seeds_user"
	disabledSeedsKey = "seeds_disabled"
)

// SettingsStore is the slice of the database that seed management needs.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Key identifies a seed: the same board is the same seed whatever its display name.
func Key(s SeedTarget) string {
	svc := strings.ToLower(strings.TrimSpace(s.ATSService))
	if svc == "" || svc == "generic" {
		return "domain:" + strings.ToLower(strings.TrimSpace(s.Domain))
	}
	return svc + ":" + strings.ToLower(strings.TrimSpace(s.ATSBoardID))
}

// Merge combines seed lists, keeping the first occurrence of each Key.
func Merge(lists ...[]SeedTarget) []SeedTarget {
	seen := map[string]bool{}
	var out []SeedTarget
	for _, l := range lists {
		for _, s := range l {
			if k := Key(s); !seen[k] {
				seen[k] = true
				out = append(out, s)
			}
		}
	}
	return out
}

func loadJSON[T any](ctx context.Context, st SettingsStore, key string) (T, error) {
	var zero T
	raw, ok, err := st.GetSetting(ctx, key)
	if err != nil || !ok || raw == "" {
		return zero, err
	}
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return zero, nil // a corrupt value is treated as unset rather than blocking scans
	}
	return v, nil
}

// User returns the seeds the user imported.
func User(ctx context.Context, st SettingsStore) ([]SeedTarget, error) {
	return loadJSON[[]SeedTarget](ctx, st, userSeedsKey)
}

// Disabled returns the keys of seeds the user (or `seeds check --prune`) turned off.
func Disabled(ctx context.Context, st SettingsStore) (map[string]bool, error) {
	keys, err := loadJSON[[]string](ctx, st, disabledSeedsKey)
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, err
}

// Effective is the catalog to scan: embedded + imported, minus disabled seeds.
func Effective(ctx context.Context, st SettingsStore) ([]SeedTarget, error) {
	embedded, err := GetEmbeddedSeeds()
	if err != nil {
		return nil, err
	}
	if st == nil {
		return embedded, nil
	}
	user, err := User(ctx, st)
	if err != nil {
		return nil, err
	}
	disabled, err := Disabled(ctx, st)
	if err != nil {
		return nil, err
	}
	var out []SeedTarget
	for _, s := range Merge(embedded, user) {
		if !disabled[Key(s)] {
			out = append(out, s)
		}
	}
	return out, nil
}

// AddUser merges seeds into the imported set and returns how many were new.
func AddUser(ctx context.Context, st SettingsStore, add []SeedTarget) (int, error) {
	existing, err := User(ctx, st)
	if err != nil {
		return 0, err
	}
	embedded, _ := GetEmbeddedSeeds()
	have := map[string]bool{}
	for _, s := range Merge(embedded, existing) {
		have[Key(s)] = true
	}
	added := 0
	for _, s := range add {
		if !have[Key(s)] {
			have[Key(s)] = true
			existing = append(existing, s)
			added++
		}
	}
	data, _ := json.Marshal(existing)
	return added, st.SetSetting(ctx, userSeedsKey, string(data))
}

// Disable turns seeds off by key (dropping them from the imported set when they came from there).
func Disable(ctx context.Context, st SettingsStore, keys []string) error {
	disabled, err := Disabled(ctx, st)
	if err != nil {
		return err
	}
	for _, k := range keys {
		disabled[k] = true
	}
	list := make([]string, 0, len(disabled))
	for k := range disabled {
		list = append(list, k)
	}
	sort.Strings(list)
	data, _ := json.Marshal(list)
	return st.SetSetting(ctx, disabledSeedsKey, string(data))
}

// Reset forgets all imported and disabled seeds.
func Reset(ctx context.Context, st SettingsStore) error {
	if err := st.SetSetting(ctx, userSeedsKey, "[]"); err != nil {
		return err
	}
	return st.SetSetting(ctx, disabledSeedsKey, "[]")
}
