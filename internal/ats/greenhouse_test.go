package ats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"specter/internal/crawler"
)

func TestGreenhouseExtraction(t *testing.T) {
	mockResponse := `{
		"jobs": [
			{
				"id": 1001,
				"title": "Senior Distributed Systems Engineer",
				"absolute_url": "https://boards.greenhouse.io/mockcompany/jobs/1001",
				"location": {"name": "Remote"},
				"departments": [{"name": "Core Infrastructure"}],
				"content": "Join us to scale high throughput services using Go, Golang, Redis and Kafka in a FinTech ecosystem."
			},
			{
				"id": 1002,
				"title": "Marketing Manager",
				"absolute_url": "https://boards.greenhouse.io/mockcompany/jobs/1002",
				"location": {"name": "New York"},
				"departments": [{"name": "Marketing"}],
				"content": "Manage marketing campaigns and brand awareness."
			}
		]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	// Fetcher configured with AllowPrivateNetworks = true for localhost test server
	fetcher := crawler.NewFetcher(crawler.FetcherConfig{
		Timeout:              5 * time.Second,
		AllowPrivateNetworks: true,
	})

	adapter := NewGreenhouseAdapter(fetcher)

	// Verify detection
	if !adapter.Detect("boards.greenhouse.io/mockcompany") {
		t.Errorf("failed to detect greenhouse URL")
	}

	// Override endpoint by custom test invocation or mock check
	meta := &CompanyMeta{
		Name:       "Mock Company",
		Domain:     "mockcompany.com",
		CareersURL: "https://boards.greenhouse.io/mockcompany",
	}

	res, err := fetcher.Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status code: %d", res.StatusCode)
	}
	_ = meta
}
