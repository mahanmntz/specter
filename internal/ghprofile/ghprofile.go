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
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
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

// DetectedSkill is a catalog skill inferred from the user's work and self-description.
type DetectedSkill struct {
	Name        string    `json:"name"`
	Score       float64   `json:"score"`
	Repos       int       `json:"repos"`
	LastUsed    time.Time `json:"last_used,omitempty"`
	FromProfile bool      `json:"from_profile"` // named in the bio or profile README
	Selected    bool      `json:"selected"`     // strong enough to pre-select
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
	ReadmeUsed    bool               `json:"readme_used"`
	Skills        []DetectedSkill    `json:"skills"`
	Role          string             `json:"role"`
	Roles         []string           `json:"roles"` // best first
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
	CreatedAt   time.Time `json:"created_at"`
	Size        int       `json:"size"` // KB
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

	// The profile README (the "overview": the special <login>/<login> repository) is how people
	// describe themselves, so it is a strong signal. It is optional.
	readme, _ := c.getRaw(ctx, "/repos/"+url.PathEscape(login)+"/"+url.PathEscape(login)+"/readme")
	res.ReadmeUsed = strings.TrimSpace(readme) != ""

	now := time.Now()
	langs := c.fetchLanguages(ctx, login, repos, now)
	analyze(res, repos, langs, user.Bio, readme, now)
	return res, nil
}

// maxLanguageLookups bounds the extra API calls: unauthenticated GitHub allows 60 requests an hour.
const maxLanguageLookups = 8

// fetchLanguages reads the per-language byte counts of the user's strongest original repositories,
// which says far more than the single "primary language" label. It is best effort: any error
// (including rate limiting) just means those repositories fall back to their primary language.
func (c *Client) fetchLanguages(ctx context.Context, login string, repos []ghRepo, now time.Time) map[string]map[string]int {
	type scored struct {
		name string
		w    float64
	}
	var cand []scored
	for _, r := range repos {
		if !r.Fork {
			cand = append(cand, scored{r.Name, repoWeight(r, now)})
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].w > cand[j].w })
	if len(cand) > maxLanguageLookups {
		cand = cand[:maxLanguageLookups]
	}

	out := map[string]map[string]int{}
	for _, r := range cand {
		if ctx.Err() != nil {
			break
		}
		var langs map[string]int
		err := c.get(ctx, "/repos/"+url.PathEscape(login)+"/"+url.PathEscape(r.name)+"/languages", &langs)
		if errors.Is(err, ErrRateLimited) {
			break // out of budget: keep what we have
		}
		if err == nil && len(langs) > 0 {
			out[r.name] = langs
		}
	}
	return out
}

// getRaw fetches a raw text document (e.g. a README); a missing document is not an error.
func (c *Client) getRaw(ctx context.Context, path string) (string, error) {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.raw+json")
	req.Header.Set("User-Agent", "specter-local")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
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
		return "", nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 200<<10))
	return string(data), err
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
