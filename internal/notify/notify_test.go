package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mahanmntz/specter/internal/ats"
)

var sample = Message{Title: "2 new roles", Roles: []Role{
	{Company: "Acme", Title: "Backend Engineer", Location: "Remote", URL: "https://acme.io/1", Score: 80, Global: true},
	{Company: "Initech", Title: "SRE", URL: "https://initech.io/2"},
}}

func TestTextFormatting(t *testing.T) {
	txt := sample.Text()
	for _, want := range []string{"2 new roles", "🌍 Acme — Backend Engineer (Remote) [80%]", "https://initech.io/2"} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in:\n%s", want, txt)
		}
	}
}

func TestWebhookSlackTelegram(t *testing.T) {
	var gotPaths []string
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		gotPaths = append(gotPaths, r.URL.Path)
		bodies = append(bodies, b)
	}))
	defer srv.Close()

	ns := []Notifier{
		&Webhook{URL: srv.URL + "/hook", Client: srv.Client()},
		&Slack{URL: srv.URL + "/slack", Client: srv.Client()},
		&Telegram{Token: "T0K", ChatID: "42", BaseURL: srv.URL, Client: srv.Client()},
	}
	if err := SendAll(context.Background(), ns, sample); err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotPaths, ",") != "/hook,/slack,/botT0K/sendMessage" {
		t.Errorf("paths = %v", gotPaths)
	}
	if len(bodies[0]["roles"].([]any)) != 2 {
		t.Errorf("webhook body = %v", bodies[0])
	}
	if !strings.Contains(bodies[1]["text"].(string), "Acme") || bodies[2]["chat_id"] != "42" {
		t.Errorf("bodies = %v", bodies)
	}
}

func TestSendAllJoinsErrors(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 500)
	}))
	defer bad.Close()
	err := SendAll(context.Background(), []Notifier{&Slack{URL: bad.URL, Client: bad.Client()}}, sample)
	if err == nil || !strings.Contains(err.Error(), "slack") || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	for _, k := range []string{"SPECTER_WEBHOOK_URL", "SPECTER_SLACK_WEBHOOK", "SPECTER_TELEGRAM_TOKEN", "SPECTER_TELEGRAM_CHAT"} {
		t.Setenv(k, "")
	}
	if len(FromEnv()) != 0 {
		t.Error("expected none")
	}
	t.Setenv("SPECTER_SLACK_WEBHOOK", "https://hooks.example/x")
	t.Setenv("SPECTER_TELEGRAM_TOKEN", "t") // chat missing -> not enabled
	if got := FromEnv(); len(got) != 1 || got[0].Name() != "slack" {
		t.Errorf("got %v", got)
	}
}

func TestMessageFromRolesRanksAndTruncates(t *testing.T) {
	roles := []ats.JobPosting{
		{CompanyName: "A", Title: "Backend Engineer", URL: "u1", Keywords: []string{"go"}},
		{CompanyName: "B", Title: "Backend Engineer", URL: "u2", ApplyURL: "apply2", GlobalRemote: true},
		{CompanyName: "C", Title: "Designer", URL: "u3"},
	}
	msg := MessageFromRoles(roles, []string{"Go", "Backend"}, 2)
	if len(msg.Roles) != 2 || msg.Roles[0].Company != "B" || msg.Roles[0].URL != "apply2" || msg.Roles[1].Company != "A" {
		t.Errorf("roles = %+v", msg.Roles)
	}
	if !strings.Contains(msg.Title, "3 new") || !strings.Contains(msg.Title, "top 2") {
		t.Errorf("title = %q", msg.Title)
	}
}
