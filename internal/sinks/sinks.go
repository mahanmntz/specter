// Package sinks pushes discovered jobs to third-party databases (Airtable, Notion).
// Pushes are idempotent: re-running updates or skips existing rows keyed by role ID.
package sinks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mahanmntz/specter/internal/ats"
)

// Sink is a destination that accepts job postings.
type Sink interface {
	Name() string
	// Push upserts the jobs and returns how many were written (created or updated).
	Push(ctx context.Context, jobs []ats.JobPosting) (int, error)
}

func link(j ats.JobPosting) string {
	if j.ApplyURL != "" {
		return j.ApplyURL
	}
	return j.URL
}

func remoteLabel(j ats.JobPosting) string {
	switch {
	case j.GlobalRemote && j.ContractorFriendly:
		return "Worldwide + Contractor"
	case j.GlobalRemote:
		return "Worldwide"
	case j.ContractorFriendly:
		return "Contractor"
	default:
		return j.RemotePolicy
	}
}

// doJSON sends a JSON request and decodes a JSON response into out (may be nil).
func doJSON(ctx context.Context, c *http.Client, method, endpoint string, headers map[string]string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%s %s: status %d: %s", method, endpoint, resp.StatusCode, msg)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func defaultClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }
