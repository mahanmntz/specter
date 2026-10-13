package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mahanmntz/specter/internal/ats"
)

type pipelineOut struct {
	Stages []struct {
		ID   string
		Jobs []Job
	}
	FollowUpsDue int `json:"follow_ups_due"`
}

func getPipeline(t *testing.T, s *Server) pipelineOut {
	t.Helper()
	rec := get(t, s.Handler(), "/api/pipeline")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out pipelineOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func stageIDs(out pipelineOut, id string) []string {
	for _, st := range out.Stages {
		if st.ID == id {
			var ids []string
			for _, j := range st.Jobs {
				ids = append(ids, j.ID)
			}
			return ids
		}
	}
	return nil
}

func TestPipelineColumnsFollowUpsAndClosedJobs(t *testing.T) {
	src := newFake()
	src.closed = []ats.JobPosting{{ID: "gone-9", Title: "Backend Engineer", CompanyName: "Gone Inc", CompanyDomain: "gone.com", Closed: true}}
	s := newServer(src)
	h := s.Handler()

	yesterday := now.Add(-24 * time.Hour).Format("2006-01-02")
	nextMonth := now.Add(30 * 24 * time.Hour).Format("2006-01-02")
	steps := []string{
		`{"id":"1","status":"applied","follow_up":"` + yesterday + `","note":"Spoke to Sam"}`, // overdue
		`{"id":"3","status":"applied","follow_up":"` + nextMonth + `"}`,                       // not due
		`{"id":"2","status":"saved"}`,
		`{"id":"gone-9","status":"interview"}`,
	}
	for _, body := range steps {
		if rec := do(t, h, "POST", "/api/status", body); rec.Code != 200 {
			t.Fatalf("%s → %d %s", body, rec.Code, rec.Body)
		}
	}

	out := getPipeline(t, s)
	if got := strings.Join(stageIDs(out, "applied"), ","); got != "1,3" { // overdue reminder sorts first
		t.Errorf("applied column = %s", got)
	}
	if got := stageIDs(out, "saved"); len(got) != 1 || got[0] != "2" {
		t.Errorf("saved = %v", got)
	}
	interview := stageIDs(out, "interview")
	if len(interview) != 1 || interview[0] != "gone-9" {
		t.Errorf("a closed posting must stay on the board: %v", interview)
	}
	for _, st := range out.Stages {
		for _, j := range st.Jobs {
			if j.ID == "gone-9" && !j.Closed {
				t.Error("closed flag missing")
			}
			if j.ID == "1" && j.Note != "Spoke to Sam" {
				t.Errorf("note = %q", j.Note)
			}
		}
	}
	if out.FollowUpsDue != 1 {
		t.Errorf("exactly one follow-up is due, got %d", out.FollowUpsDue)
	}
	if len(out.Stages) != 5 {
		t.Errorf("five board columns expected, got %d", len(out.Stages))
	}

	// Facets surface the same reminder count.
	var f struct {
		FollowUps int `json:"follow_ups"`
	}
	_ = json.Unmarshal(get(t, h, "/api/facets").Body.Bytes(), &f)
	if f.FollowUps != 1 {
		t.Errorf("facets follow_ups = %d", f.FollowUps)
	}
}

func TestRejectedJobsLeaveTheMainListButStayOnTheBoard(t *testing.T) {
	s := newServer(newFake())
	h := s.Handler()
	do(t, h, "POST", "/api/status", `{"id":"1","status":"rejected"}`)

	_, ids := jobsFor(t, s, "")
	for _, id := range ids {
		if id == "1" {
			t.Error("a rejected job should not clutter the main list")
		}
	}
	if got := stageIDs(getPipeline(t, s), "rejected"); len(got) != 1 {
		t.Errorf("rejected column = %v", got)
	}
	if _, ids := jobsFor(t, s, "status=rejected"); len(ids) != 1 {
		t.Errorf("status=rejected view = %v", ids)
	}
}

func TestStatusEndpointValidatesNotesAndDates(t *testing.T) {
	h := newServer(newFake()).Handler()
	cases := map[string]int{
		`{"id":"1","follow_up":"not-a-date"}`:                   http.StatusBadRequest,
		`{"id":"1","status":"interview"}`:                       http.StatusOK,
		`{"id":"1","note":"hello"}`:                             http.StatusOK,
		`{"id":"1","follow_up":""}`:                             http.StatusOK, // clears the reminder
		`{"id":"1","status":"nonsense"}`:                        http.StatusBadRequest,
		`{"status":"saved"}`:                                    http.StatusBadRequest,
		`{"id":"1","note":"` + strings.Repeat("x", 5000) + `"}`: http.StatusBadRequest,
	}
	for body, want := range cases {
		if rec := do(t, h, "POST", "/api/status", body); rec.Code != want {
			t.Errorf("%.60s… → %d, want %d", body, rec.Code, want)
		}
	}
}
