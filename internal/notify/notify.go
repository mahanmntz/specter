// Package notify delivers new-job alerts to external services.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Role is the minimal job summary included in an alert.
type Role struct {
	Company  string `json:"company"`
	Title    string `json:"title"`
	Location string `json:"location,omitempty"`
	URL      string `json:"url"`
	Score    int    `json:"score,omitempty"`
	Global   bool   `json:"global_remote,omitempty"`
}

// Message is one alert.
type Message struct {
	Title string `json:"title"`
	Roles []Role `json:"roles"`
}

// Notifier delivers a Message to one destination.
type Notifier interface {
	Name() string
	Send(ctx context.Context, msg Message) error
}

// Text renders msg as plain text (used by chat-style notifiers).
func (m Message) Text() string {
	var b strings.Builder
	b.WriteString(m.Title)
	for _, r := range m.Roles {
		b.WriteString("\n• ")
		if r.Global {
			b.WriteString("🌍 ")
		}
		fmt.Fprintf(&b, "%s — %s", r.Company, r.Title)
		if r.Location != "" {
			fmt.Fprintf(&b, " (%s)", r.Location)
		}
		if r.Score > 0 {
			fmt.Fprintf(&b, " [%d%%]", r.Score)
		}
		fmt.Fprintf(&b, "\n  %s", r.URL)
	}
	return b.String()
}

// FromEnv builds the notifiers configured through environment variables:
//
//	SPECTER_WEBHOOK_URL     generic JSON POST ({"title":..., "roles":[...]})
//	SPECTER_SLACK_WEBHOOK   Slack/Mattermost-compatible incoming webhook
//	SPECTER_TELEGRAM_TOKEN + SPECTER_TELEGRAM_CHAT   Telegram bot
func FromEnv() []Notifier {
	client := &http.Client{Timeout: 15 * time.Second}
	var out []Notifier
	if u := os.Getenv("SPECTER_WEBHOOK_URL"); u != "" {
		out = append(out, &Webhook{URL: u, Client: client})
	}
	if u := os.Getenv("SPECTER_SLACK_WEBHOOK"); u != "" {
		out = append(out, &Slack{URL: u, Client: client})
	}
	if t, c := os.Getenv("SPECTER_TELEGRAM_TOKEN"), os.Getenv("SPECTER_TELEGRAM_CHAT"); t != "" && c != "" {
		out = append(out, &Telegram{Token: t, ChatID: c, Client: client, BaseURL: "https://api.telegram.org"})
	}
	return out
}

// SendAll delivers msg to every notifier and joins any errors.
func SendAll(ctx context.Context, ns []Notifier, msg Message) error {
	var errs []error
	for _, n := range ns {
		if err := n.Send(ctx, msg); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Name(), err))
		}
	}
	return errors.Join(errs...)
}

func post(ctx context.Context, c *http.Client, endpoint string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// Webhook POSTs the Message as JSON.
type Webhook struct {
	URL    string
	Client *http.Client
}

func (w *Webhook) Name() string { return "webhook" }
func (w *Webhook) Send(ctx context.Context, msg Message) error {
	return post(ctx, w.Client, w.URL, msg)
}

// Slack posts to a Slack-compatible incoming webhook.
type Slack struct {
	URL    string
	Client *http.Client
}

func (s *Slack) Name() string { return "slack" }
func (s *Slack) Send(ctx context.Context, msg Message) error {
	return post(ctx, s.Client, s.URL, map[string]string{"text": msg.Text()})
}

// Telegram sends through the Bot API.
type Telegram struct {
	Token, ChatID, BaseURL string
	Client                 *http.Client
}

func (t *Telegram) Name() string { return "telegram" }
func (t *Telegram) Send(ctx context.Context, msg Message) error {
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", t.BaseURL, url.PathEscape(t.Token))
	return post(ctx, t.Client, endpoint, map[string]any{
		"chat_id":                  t.ChatID,
		"text":                     msg.Text(),
		"disable_web_page_preview": true,
	})
}
