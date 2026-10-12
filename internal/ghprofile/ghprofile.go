// Package ghprofile reads a person's public GitHub profile and infers the
// skills they use and the engineering role they lean toward. It only reads
// public data (one profile call and one repository-list call).
package ghprofile

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mahanmntz/specter/internal/signals"
)

// Errors returned by Analyze.
var (
	ErrInvalidLogin = errors.New("that doesn't look like a GitHub username")
	ErrNotFound     = errors.New("GitHub user not found")
	ErrUnreachable  = errors.New("couldn't reach GitHub; check your connection and try again")
	ErrRateLimited  = errors.New("GitHub rate limit reached; try again in a few minutes (or set GITHUB_TOKEN)")
)

// Client talks to the GitHub REST API.
type Client struct {
	HTTP    *http.Client
	BaseURL string // defaults to https://api.github.com
	Token   string // optional; defaults to $GITHUB_TOKEN
	// AllowAnyHost lets AvatarDataURL fetch from any host (tests only).
	AllowAnyHost bool
}

// NewClient returns a Client with sane defaults.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}, BaseURL: "https://api.github.com", Token: os.Getenv("GITHUB_TOKEN")}
}

// DetectedSkill is a catalog skill inferred from the user's repositories.
type DetectedSkill struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	Repos int     `json:"repos"`
}

// Result is what Analyze learned about a user.
type Result struct {
	Login         string             `json:"login"`
	Name          string             `json:"name"`
	Bio           string             `json:"bio"`
	Location      string             `json:"location"`
	Company       string             `json:"company"`
	AvatarURL     string             `json:"avatar_url"`
	PublicRepos   int                `json:"public_repos"`
	Followers     int                `json:"followers"`
	ReposAnalyzed int                `json:"repos_analyzed"`
	Skills        []DetectedSkill    `json:"skills"`
	Role          string             `json:"role"`
	RoleScores    map[string]float64 `json:"role_scores"`
}

var loginRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// ParseLogin accepts "octocat", "@octocat" or a github.com profile URL.
func ParseLogin(input string) (string, error) {
	s := strings.TrimSpace(input)
	s = strings.TrimPrefix(s, "@")
	if strings.Contains(s, "github.com") {
		if !strings.Contains(s, "://") {
			s = "https://" + s
		}
		u, err := url.Parse(s)
		if err != nil || !strings.HasSuffix(strings.ToLower(u.Host), "github.com") {
			return "", ErrInvalidLogin
		}
		s = strings.Split(strings.Trim(u.Path, "/"), "/")[0]
	}
	if !loginRe.MatchString(s) || strings.HasSuffix(s, "-") {
		return "", ErrInvalidLogin
	}
	return s, nil
}

type ghUser struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	Bio       string `json:"bio"`
	Location  string `json:"location"`
	Company   string `json:"company"`
	AvatarURL string `json:"avatar_url"`
	Repos     int    `json:"public_repos"`
	Followers int    `json:"followers"`
}

type ghRepo struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Language    string    `json:"language"`
	Topics      []string  `json:"topics"`
	Stars       int       `json:"stargazers_count"`
	Fork        bool      `json:"fork"`
	Archived    bool      `json:"archived"`
	PushedAt    time.Time `json:"pushed_at"`
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "specter-local")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil && ctx.Err() == nil {
		// One quick retry: transient connection resets are common on flaky networks.
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
		}
		resp, err = client.Do(req.Clone(ctx))
	}
	if err != nil {
		return ErrUnreachable
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return ErrRateLimited
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("GitHub returned status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// Analyze fetches the user's public profile and repositories and infers skills and role.
func (c *Client) Analyze(ctx context.Context, login string) (*Result, error) {
	login, err := ParseLogin(login)
	if err != nil {
		return nil, err
	}
	var user ghUser
	if err := c.get(ctx, "/users/"+url.PathEscape(login), &user); err != nil {
		return nil, err
	}
	var repos []ghRepo
	if err := c.get(ctx, "/users/"+url.PathEscape(login)+"/repos?per_page=100&sort=pushed&type=owner", &repos); err != nil {
		return nil, err
	}

	res := &Result{
		Login: user.Login, Name: user.Name, Bio: user.Bio, Location: user.Location, Company: user.Company,
		AvatarURL: user.AvatarURL, PublicRepos: user.Repos, Followers: user.Followers,
	}
	weights, repoCounts, analyzed := weigh(repos, user.Bio, time.Now())
	res.ReposAnalyzed = analyzed
	res.RoleScores = signals.RoleScores(weights)
	res.Role = bestRole(res.RoleScores)

	for name, w := range weights {
		res.Skills = append(res.Skills, DetectedSkill{Name: name, Score: math.Round(w*10) / 10, Repos: repoCounts[name]})
	}
	sort.Slice(res.Skills, func(i, j int) bool {
		if res.Skills[i].Score != res.Skills[j].Score {
			return res.Skills[i].Score > res.Skills[j].Score
		}
		return res.Skills[i].Name < res.Skills[j].Name
	})
	return res, nil
}

// languageSkills maps GitHub's language labels onto catalog skills (beyond exact-name matches).
var languageSkills = map[string][]string{
	"Jupyter Notebook": {"Python", "Machine Learning"},
	"HCL":              {"Terraform"},
	"Dockerfile":       {"Docker"},
	"PLpgSQL":          {"PostgreSQL", "SQL"},
	"TSQL":             {"SQL"},
	"SCSS":             {"CSS"},
	"Objective-C":      {"iOS"},
	"Vue":              {"Vue"},
	"Svelte":           {"Svelte"},
	"Smarty":           {"Helm"},
}

// weigh turns repositories into per-skill weights. Recent, starred, original work counts more;
// topics are a stronger signal than the primary language, which is stronger than description mentions.
func weigh(repos []ghRepo, bio string, now time.Time) (weights map[string]float64, repoCounts map[string]int, analyzed int) {
	weights = map[string]float64{}
	repoCounts = map[string]int{}

	originals := 0
	for _, r := range repos {
		if !r.Fork {
			originals++
		}
	}

	for _, r := range repos {
		// Forks are mostly other people's code; ignore them unless that is all there is.
		if r.Fork && originals > 0 {
			continue
		}
		analyzed++
		w := 1.0
		if age := now.Sub(r.PushedAt); age < 365*24*time.Hour {
			w += 1
		} else if age > 3*365*24*time.Hour {
			w -= 0.5
		}
		if r.Archived {
			w -= 0.5
		}
		w += 0.5 * math.Log2(1+float64(r.Stars))
		if w < 0.25 {
			w = 0.25
		}

		hit := map[string]float64{}
		add := func(skill string, factor float64) {
			if v := w * factor; v > hit[skill] {
				hit[skill] = v
			}
		}
		if r.Language != "" {
			if canon, ok := signals.CanonicalSkill(r.Language); ok {
				add(canon, 1)
			}
			for _, s := range languageSkills[r.Language] {
				add(s, 1)
			}
		}
		for _, topic := range r.Topics {
			if canon, ok := signals.CanonicalSkill(topic); ok {
				add(canon, 1.5)
			}
		}
		for _, s := range signals.FindSkills(r.Description + " " + strings.ReplaceAll(r.Name, "-", " ")) {
			add(s, 0.5)
		}
		for skill, v := range hit {
			weights[skill] += v
			repoCounts[skill]++
		}
	}
	// The bio is self-declared, so give each skill it names a solid nudge.
	for _, s := range signals.FindSkills(bio) {
		weights[s] += 2
	}
	return weights, repoCounts, analyzed
}

// bestRole picks the preset with the highest score; backend wins ties and the no-signal case.
func bestRole(scores map[string]float64) string {
	best, bestScore := "backend", scores["backend"]
	for _, p := range signals.Presets() {
		if s := scores[p.ID]; s > bestScore {
			best, bestScore = p.ID, s
		}
	}
	return best
}

// AvatarDataURL downloads a (small) GitHub avatar and returns it as a data: URL so the UI never
// needs to contact GitHub's image host. Only GitHub avatar hosts are fetched unless AllowAnyHost is set.
func (c *Client) AvatarDataURL(ctx context.Context, avatarURL string) (string, error) {
	u, err := url.Parse(avatarURL)
	if err != nil || u.Scheme != "https" && !c.AllowAnyHost {
		return "", fmt.Errorf("unsupported avatar url")
	}
	if !c.AllowAnyHost && u.Host != "avatars.githubusercontent.com" {
		return "", fmt.Errorf("unsupported avatar host %q", u.Host)
	}
	q := u.Query()
	q.Set("s", "96")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "specter-local")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("avatar status %d", resp.StatusCode)
	}
	ct := strings.ToLower(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/gif" && ct != "image/webp" {
		return "", fmt.Errorf("unexpected avatar type %q", ct)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 300<<10+1))
	if err != nil || len(data) > 300<<10 {
		return "", fmt.Errorf("avatar too large or unreadable")
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
