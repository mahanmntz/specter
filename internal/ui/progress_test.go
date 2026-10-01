package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestProgressTracker(t *testing.T) {
	buf := &bytes.Buffer{}
	tracker := NewProgressTracker(buf)

	tracker.Start(10 * time.Millisecond)
	tracker.SetActiveWorkers(3)
	tracker.SetTotalURLs(10)
	tracker.IncCrawled()
	tracker.IncLeads(2)
	tracker.IncEmails(2)
	tracker.SetRateLimitStatus("Cooling down")

	time.Sleep(35 * time.Millisecond)
	tracker.Log("Found new repo: stripe/stripe-go")
	time.Sleep(20 * time.Millisecond)
	tracker.Stop()

	output := buf.String()
	if !strings.Contains(output, "Found new repo: stripe/stripe-go") {
		t.Errorf("expected log message in output, got: %s", output)
	}
	if !strings.Contains(output, "[Specter]") {
		t.Errorf("expected [Specter] progress tag in output, got: %s", output)
	}
}
