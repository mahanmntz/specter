package sinks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"specter/internal/ats"
)

func jobs(n int) []ats.JobPosting {
	out := make([]ats.JobPosting, n)
	for i := range out {
		out[i] = ats.JobPosting{
			ID: "gh-" + string(rune('a'+i)), Title: "Backend Engineer", CompanyName: "Acme", CompanyDomain: "acme.com",
			URL: "https://acme.com/j", ApplyURL: "https://acme.com/apply", Keywords: []string{"go", "redis"},
			GlobalRemote: true, ContractorFriendly: true,
		}
	}
	return out
}

func TestAirtableBatchesAndUpserts(t *testing.T) {
	var batches []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v0/appBASE/Jobs" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("bad request %s %s auth=%s", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			PerformUpsert struct {
				Fields []string `json:"fieldsToMergeOn"`
			} `json:"performUpsert"`
			Records []struct {
				Fields map[string]any `json:"fields"`
			} `json:"records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.PerformUpsert.Fields) != 1 || body.PerformUpsert.Fields[0] != "ID" {
			t.Errorf("upsert = %+v", body.PerformUpsert)
		}
		if body.Records[0].Fields["URL"] != "https://acme.com/apply" || body.Records[0].Fields["Remote"] != "Worldwide + Contractor" {
			t.Errorf("fields = %v", body.Records[0].Fields)
		}
		batches = append(batches, len(body.Records))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	a := &Airtable{Token: "tok", BaseID: "appBASE", Table: "Jobs", BaseURL: srv.URL, Client: srv.Client()}
	n, err := a.Push(context.Background(), jobs(23))
	if err != nil || n != 23 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(batches) != 3 || batches[0] != 10 || batches[2] != 3 {
		t.Errorf("batches = %v", batches)
	}
}

func TestAirtableSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"NOT_FOUND"}`, 404)
	}))
	defer srv.Close()
	a := &Airtable{Token: "t", BaseID: "b", Table: "Jobs", BaseURL: srv.URL, Client: srv.Client()}
	if _, err := a.Push(context.Background(), jobs(1)); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v", err)
	}
}

func TestNotionSkipsExisting(t *testing.T) {
	created := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Notion-Version") == "" {
			t.Error("missing Notion-Version")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/query"):
			var body struct {
				Filter struct {
					RichText struct {
						Equals string `json:"equals"`
					} `json:"rich_text"`
				} `json:"filter"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Filter.RichText.Equals == "gh-a@acme.com" {
				_, _ = w.Write([]byte(`{"results":[{"id":"p1"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"results":[]}`))
			}
		case r.URL.Path == "/v1/pages":
			created++
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	n := &Notion{Token: "t", DatabaseID: "db1", BaseURL: srv.URL, Client: srv.Client()}
	got, err := n.Push(context.Background(), jobs(3))
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 || created != 2 {
		t.Errorf("got=%d created=%d", got, created)
	}
}

func TestFromEnvRequiresCredentials(t *testing.T) {
	for _, k := range []string{"AIRTABLE_TOKEN", "AIRTABLE_BASE", "NOTION_TOKEN", "NOTION_DATABASE_ID"} {
		t.Setenv(k, "")
	}
	if _, err := NewAirtableFromEnv(); err == nil {
		t.Error("airtable should require creds")
	}
	if _, err := NewNotionFromEnv(); err == nil {
		t.Error("notion should require creds")
	}
	t.Setenv("AIRTABLE_TOKEN", "t")
	t.Setenv("AIRTABLE_BASE", "b")
	a, err := NewAirtableFromEnv()
	if err != nil || a.Table != "Jobs" {
		t.Errorf("a=%+v err=%v", a, err)
	}
}
