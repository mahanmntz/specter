package feeds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"specter/internal/crawler"
)

func fetcher() *crawler.Fetcher {
	return crawler.NewFetcher(crawler.FetcherConfig{Timeout: 5 * time.Second, AllowPrivateNetworks: true})
}

func TestRemoteOKFetchGroupsAndFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
		 {"legal":"notice"},
		 {"id":"1","epoch":1790000000,"company":"Acme Corp","position":"Senior Backend Engineer","tags":["golang","postgres"],"description":"Go services with Kafka","location":"Worldwide","apply_url":"https://acme.io/jobs/1"},
		 {"id":"2","epoch":1790000000,"company":"Acme Corp","position":"Python Backend Developer","tags":["python"],"description":"Django and PostgreSQL","url":"https://remoteok.com/remote-jobs/2"},
		 {"id":"3","company":"Brandly","position":"Social Media Manager","tags":["marketing"],"description":"posts"}
		]`))
	}))
	defer srv.Close()

	feed := NewRemoteOK(fetcher())
	feed.apiURL = srv.URL
	metas, err := feed.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 {
		t.Fatalf("want 1 company with relevant roles, got %d", len(metas))
	}
	m := metas[0]
	if m.Name != "Acme Corp" || m.Domain != "acme.io" || len(m.OpenRoles) != 2 {
		t.Errorf("unexpected meta: %+v", m)
	}
	if !strings.HasPrefix(m.OpenRoles[0].ID, "remoteok-") {
		t.Errorf("id = %s", m.OpenRoles[0].ID)
	}
}

func TestParseHNPost(t *testing.T) {
	raw := `Acme | Senior Backend Engineer (Go) | REMOTE (Worldwide) | $150k<p>We build things with Go &amp; Postgres. Apply: <a href="https://acme.io/careers">https://acme.io/careers</a>`
	company, job, ok := parseHNPost(raw)
	if !ok || company != "Acme" {
		t.Fatalf("ok=%v company=%q", ok, company)
	}
	if job.Title != "Senior Backend Engineer (Go)" || job.Workplace != "remote" || job.URL != "https://acme.io/careers" {
		t.Errorf("job = %+v", job)
	}
	if !strings.Contains(job.Description, "Go & Postgres") {
		t.Errorf("html not unescaped: %q", job.Description)
	}

	for _, bad := range []string{"", "just a comment with no pipes", "Acme | Marketing Lead | NYC"} {
		if _, _, ok := parseHNPost(bad); ok {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestHackerNewsFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search_by_date", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hits":[{"objectID":"42","title":"Ask HN: Who is hiring? (October 2026)"}]}`))
	})
	mux.HandleFunc("/items/42", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"children":[
		  {"id":100,"created_at":"2026-10-01T10:00:00Z","text":"Initech | Backend Engineer | Remote | Node.js, PostgreSQL<p>Build APIs."},
		  {"id":101,"created_at":"2026-10-01T11:00:00Z","text":"not a job post"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	feed := NewHackerNews(fetcher())
	feed.apiBase = srv.URL
	metas, err := feed.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Name != "Initech" || metas[0].Domain != "initech.unknown" {
		t.Fatalf("metas = %+v", metas)
	}
	if got := metas[0].OpenRoles[0].URL; got != "https://news.ycombinator.com/item?id=100" {
		t.Errorf("url = %s", got)
	}
}

func TestNames(t *testing.T) {
	if got := strings.Join(Names(), ","); got != "hn,remoteok" {
		t.Errorf("names = %s", got)
	}
}
