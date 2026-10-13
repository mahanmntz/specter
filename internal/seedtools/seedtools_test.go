package seedtools

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mahanmntz/specter/internal/crawler"
	"github.com/mahanmntz/specter/internal/seeds"
)

type resp struct {
	status int
	body   string
	err    error
}

// fakeFetcher answers from a URL → response table; unknown URLs are 404.
type fakeFetcher struct {
	table map[string]resp
	calls atomic.Int32
}

func (f *fakeFetcher) Fetch(_ context.Context, u string) (*crawler.FetchResult, error) {
	f.calls.Add(1)
	r, ok := f.table[u]
	if !ok {
		return &crawler.FetchResult{StatusCode: 404}, nil
	}
	if r.err != nil {
		return nil, r.err
	}
	return &crawler.FetchResult{StatusCode: r.status, Body: []byte(r.body)}, nil
}

const (
	ghURL    = "https://boards-api.greenhouse.io/v1/boards/%s/jobs"
	leverURL = "https://api.lever.co/v0/postings/%s"
)

func gh(id string) string    { return strings.Replace(ghURL, "%s", id, 1) }
func lever(id string) string { return strings.Replace(leverURL, "%s", id, 1) }

func TestDetectATS(t *testing.T) {
	cases := []struct{ in, svc, id string }{
		{"https://boards.greenhouse.io/stripe", "greenhouse", "stripe"},
		{"https://job-boards.greenhouse.io/acme/jobs/123", "greenhouse", "acme"},
		{"https://boards.eu.greenhouse.io/eucorp", "greenhouse", "eucorp"},
		{"https://boards.greenhouse.io/embed/job_board?for=widgets", "greenhouse", "widgets"},
		{"jobs.lever.co/netflix", "lever", "netflix"},
		{"https://jobs.ashbyhq.com/linear", "ashby", "linear"},
		{"https://apply.workable.com/acme/", "workable", "acme"},
		{"https://acme.workable.com", "workable", "acme"},
		{"https://careers.smartrecruiters.com/Acme", "smartrecruiters", "Acme"},
		{"https://acme.recruitee.com/", "recruitee", "acme"},
		{"https://stripe.com/jobs", "", ""},
		{"https://workday.com/careers", "", ""},
		{"", "", ""},
		{"not a url at all %%", "", ""},
	}
	for _, c := range cases {
		if svc, id := DetectATS(c.in); svc != c.svc || id != c.id {
			t.Errorf("%q → (%q,%q), want (%q,%q)", c.in, svc, id, c.svc, c.id)
		}
	}
}

func TestCheckOneClassifiesBoards(t *testing.T) {
	f := &fakeFetcher{table: map[string]resp{
		gh("good"):                          {200, `{"jobs":[]}`, nil},
		gh("html"):                          {200, `<html>marketing page</html>`, nil},
		gh("down"):                          {503, ``, nil},
		gh("broken"):                        {0, ``, errors.New("connection reset")},
		gh("gone"):                          {410, ``, nil},
		leverURL[:len(leverURL)-2] + "ok":   {200, `[]`, nil},
		leverURL[:len(leverURL)-2] + "text": {200, `<html>`, nil},
		"https://api.smartrecruiters.com/v1/companies/empty/postings": {200, `{"totalFound":0,"content":[]}`, nil},
		"https://api.smartrecruiters.com/v1/companies/real/postings":  {200, `{"totalFound":3,"content":[{}]}`, nil},
	}}
	seed := func(svc, id string) seeds.SeedTarget {
		return seeds.SeedTarget{Name: id, ATSService: svc, ATSBoardID: id}
	}
	cases := []struct {
		s    seeds.SeedTarget
		want Status
	}{
		{seed("greenhouse", "good"), StatusOK},
		{seed("greenhouse", "missing"), StatusStale},
		{seed("greenhouse", "html"), StatusStale},
		{seed("greenhouse", "gone"), StatusStale},
		{seed("greenhouse", "down"), StatusUnreachable},
		{seed("greenhouse", "broken"), StatusUnreachable},
		{seed("lever", "ok"), StatusOK},
		{seed("lever", "text"), StatusStale},
		{seed("smartrecruiters", "empty"), StatusStale},
		{seed("smartrecruiters", "real"), StatusOK},
		{seeds.SeedTarget{Name: "Site", Domain: "x.com", ATSService: "generic"}, StatusSkipped},
	}
	for _, c := range cases {
		if got := CheckOne(context.Background(), f, c.s); got.Status != c.want {
			t.Errorf("%s/%s → %s, want %s", c.s.ATSService, c.s.ATSBoardID, got.Status, c.want)
		}
	}
}

func TestCheckKeepsOrderAndReportsProgress(t *testing.T) {
	f := &fakeFetcher{table: map[string]resp{gh("a"): {200, `{"jobs":[]}`, nil}, gh("c"): {200, `{"jobs":[]}`, nil}}}
	list := []seeds.SeedTarget{
		{Name: "A", ATSService: "greenhouse", ATSBoardID: "a"},
		{Name: "B", ATSService: "greenhouse", ATSBoardID: "b"},
		{Name: "C", ATSService: "greenhouse", ATSBoardID: "c"},
	}
	var lastDone int
	got := Check(context.Background(), f, list, 2, func(done, total int) { lastDone = done })
	if got[0].Status != StatusOK || got[1].Status != StatusStale || got[2].Status != StatusOK || got[1].Seed.Name != "B" {
		t.Errorf("results = %+v", got)
	}
	if lastDone != 3 {
		t.Errorf("progress = %d", lastDone)
	}
}

func TestDiscoverFindsBoardsAndBoundsProbes(t *testing.T) {
	f := &fakeFetcher{table: map[string]resp{lever("cockroachlabs"): {200, `[]`, nil}}}
	svc, id, ok := Discover(context.Background(), f, "Cockroach Labs", "cockroachlabs.com")
	if !ok || svc != "lever" || id != "cockroachlabs" {
		t.Errorf("got %s %s %v", svc, id, ok)
	}

	none := &fakeFetcher{table: map[string]resp{}}
	if _, _, ok := Discover(context.Background(), none, "Nobody Inc", "nobody.com"); ok {
		t.Error("nothing should be found")
	}
	if none.calls.Load() > 9 {
		t.Errorf("at most 9 probes per company, made %d", none.calls.Load())
	}
}

func TestParseFrontmatterAndSeedFor(t *testing.T) {
	doc := "---\ntitle: \"Stripe\"\nslug: stripe\nwebsite: https://www.Stripe.com/\ncareers_url: https://boards.greenhouse.io/stripe\nregion: north america\nremote_policy: remote-friendly\ntechnologies:\n  - ruby\n---\n\n## Blurb\ntitle: not this"
	c := ParseFrontmatter(doc)
	if c.Title != "Stripe" || c.RemotePolicy != "remote-friendly" || c.Region != "north america" {
		t.Errorf("company = %+v", c)
	}
	s, ok := SeedFor(c)
	if !ok || s.ATSService != "greenhouse" || s.ATSBoardID != "stripe" || s.Domain != "stripe.com" || s.Priority != 2 {
		t.Errorf("seed = %+v ok=%v", s, ok)
	}
	if _, ok := SeedFor(Company{Title: "Own", CareersURL: "https://own.com/jobs"}); ok {
		t.Error("a company's own careers page is not an ATS board")
	}
	if ParseFrontmatter("no frontmatter here").Title != "" {
		t.Error("documents without frontmatter yield nothing")
	}
}

func TestBuildSeedsKeepsOnlyValidBoards(t *testing.T) {
	f := &fakeFetcher{table: map[string]resp{
		gh("alive"):         {200, `{"jobs":[]}`, nil},
		lever("hidden-gem"): {200, `[]`, nil},
	}}
	companies := []Company{
		{Title: "Alive", Website: "https://alive.com", CareersURL: "https://boards.greenhouse.io/alive"},
		{Title: "Dead", Website: "https://dead.com", CareersURL: "https://boards.greenhouse.io/dead"},
		{Title: "Hidden Gem", Website: "https://hidden-gem.io", CareersURL: "https://hidden-gem.io/careers"},
	}
	sum := BuildSeeds(context.Background(), f, companies, false, 2, nil)
	if sum.Companies != 3 || sum.Detected != 2 || len(sum.Valid) != 1 || sum.Valid[0].Name != "Alive" {
		t.Errorf("without discover: %+v", sum)
	}
	sum = BuildSeeds(context.Background(), f, companies, true, 2, nil)
	if len(sum.Valid) != 2 || sum.Discovered != 1 {
		t.Errorf("with discover: %+v", sum)
	}
}

func TestParseArchiveReadsOnlyCompanyEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	add("remote-jobs-main/src/companies/stripe.md", "---\ntitle: \"Stripe\"\nwebsite: https://stripe.com\ncareers_url: https://boards.greenhouse.io/stripe\n---\nbody")
	add("remote-jobs-main/src/companies/acme.md", "---\ntitle: Acme\nwebsite: acme.io\ncareers_url: https://acme.io/jobs\n---")
	add("remote-jobs-main/src/blog/post.md", "---\ntitle: A blog post\n---")
	add("remote-jobs-main/src/companies/notes.txt", "ignored")
	add("remote-jobs-main/src/companies/broken.md", "no frontmatter")
	_ = zw.Close()

	got, err := ParseArchive(buf.Bytes())
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d companies, err=%v: %+v", len(got), err, got)
	}
	if _, err := ParseArchive([]byte("not a zip")); err == nil {
		t.Error("garbage must be an error")
	}

	f := &fakeFetcher{table: map[string]resp{remoteIntechZip: {200, buf.String(), nil}}}
	if cs, err := FetchRemoteIntech(context.Background(), f); err != nil || len(cs) != 2 {
		t.Errorf("fetch: %v %d", err, len(cs))
	}
	if _, err := FetchRemoteIntech(context.Background(), &fakeFetcher{table: map[string]resp{}}); err == nil {
		t.Error("a 404 must be reported")
	}
}
