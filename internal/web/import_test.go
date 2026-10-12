package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mahanmntz/specter/internal/ghprofile"
)

type fakeGH struct {
	res *ghprofile.Result
	err error
}

func (f fakeGH) Analyze(_ context.Context, login string) (*ghprofile.Result, error) {
	return f.res, f.err
}
func (f fakeGH) AvatarDataURL(context.Context, string) (string, error) {
	return "data:image/png;base64,AAAA", nil
}

func importServer(t *testing.T, gh GitHubAnalyzer) *Server {
	t.Helper()
	s := New(newFake(), Options{GitHub: gh})
	t.Cleanup(s.Close)
	return s
}

func TestGitHubImportPassesThroughRoleSkillsAndREADMEFlag(t *testing.T) {
	res := &ghprofile.Result{
		Login: "octocat", Name: "", Bio: "hi", AvatarURL: "https://avatars.githubusercontent.com/u/1",
		Role: "devops", Roles: []string{"devops", "software-engineer", "backend"}, ReadmeUsed: true,
		Skills: []ghprofile.DetectedSkill{
			{Name: "Terraform", Score: 5, Repos: 3, Selected: true, FromProfile: true},
			{Name: "Go", Score: 3, Repos: 2, Selected: true},
			{Name: "Shell", Score: 1.0, Repos: 1},
		},
		ReposAnalyzed: 7,
	}
	rec := do(t, importServer(t, fakeGH{res: res}).Handler(), "POST", "/api/github/import", `{"input":"octocat"}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Name       string
		Avatar     string
		Role       string
		Roles      []string
		Skills     []importSkill
		ReadmeUsed bool `json:"readme_used"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Name != "octocat" { // falls back to the login when the profile has no display name
		t.Errorf("name = %q", out.Name)
	}
	if out.Role != "devops" || len(out.Roles) != 3 || out.Roles[1] != "software-engineer" || !out.ReadmeUsed {
		t.Errorf("roles = %v (%s) readme=%v", out.Roles, out.Role, out.ReadmeUsed)
	}
	if out.Avatar == "" {
		t.Error("avatar should be inlined as a data URL")
	}
	if !out.Skills[0].Selected || !out.Skills[0].FromProfile || !out.Skills[1].Selected || out.Skills[2].Selected {
		t.Errorf("selection comes from the analyzer: %+v", out.Skills)
	}
}

func TestGitHubImportErrorMapping(t *testing.T) {
	cases := map[error]int{
		ghprofile.ErrInvalidLogin: http.StatusBadRequest,
		ghprofile.ErrNotFound:     http.StatusNotFound,
		ghprofile.ErrRateLimited:  http.StatusTooManyRequests,
		context.DeadlineExceeded:  http.StatusBadGateway,
	}
	for err, want := range cases {
		rec := do(t, importServer(t, fakeGH{err: err}).Handler(), "POST", "/api/github/import", `{"input":"x"}`)
		if rec.Code != want {
			t.Errorf("%v → %d, want %d", err, rec.Code, want)
		}
	}
	if rec := do(t, importServer(t, nil).Handler(), "POST", "/api/github/import", `{}`); rec.Code != http.StatusNotImplemented {
		t.Errorf("disabled → %d", rec.Code)
	}
	if rec := do(t, importServer(t, fakeGH{}).Handler(), "POST", "/api/github/import", `{`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json → %d", rec.Code)
	}
}

func TestPrefsIdentityFieldsAreValidated(t *testing.T) {
	restoreProfile(t)
	h := newServer(newFake()).Handler()

	good := `{"role":"backend","name":"  Ada Lovelace ","github":"https://github.com/ada","avatar":"data:image/png;base64,AAAA"}`
	if rec := do(t, h, "PUT", "/api/profile", good); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got struct {
		Prefs Prefs
	}
	_ = json.Unmarshal(do(t, h, "GET", "/api/profile", "").Body.Bytes(), &got)
	if got.Prefs.Name != "Ada Lovelace" || got.Prefs.GitHub != "ada" || !strings.HasPrefix(got.Prefs.Avatar, "data:image/") {
		t.Errorf("prefs = %+v", got.Prefs)
	}

	// A remote avatar URL is dropped (the UI must not load third-party images); bad usernames are rejected.
	do(t, h, "PUT", "/api/profile", `{"role":"backend","avatar":"https://evil.example/x.png"}`)
	got.Prefs = Prefs{} // Unmarshal does not clear fields missing from the JSON
	_ = json.Unmarshal(do(t, h, "GET", "/api/profile", "").Body.Bytes(), &got)
	if got.Prefs.Avatar != "" {
		t.Errorf("avatar = %q", got.Prefs.Avatar)
	}
	if rec := do(t, h, "PUT", "/api/profile", `{"role":"backend","github":"not valid!"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad github → %d", rec.Code)
	}
}

func TestProfileExposesCatalogAndImportFlag(t *testing.T) {
	var out struct {
		CanImport bool `json:"can_import"`
		Catalog   []struct {
			Category string
			Skills   []string
		}
	}
	_ = json.Unmarshal(do(t, importServer(t, fakeGH{}).Handler(), "GET", "/api/profile", "").Body.Bytes(), &out)
	if !out.CanImport || len(out.Catalog) < 5 || len(out.Catalog[0].Skills) == 0 {
		t.Errorf("out = %+v", out)
	}
}
