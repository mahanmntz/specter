package ats

import (
	"context"
	"time"
)

// JobPosting represents an active open technical role discovered on an ATS.
type JobPosting struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	URL                string    `json:"url"`
	ApplyURL           string    `json:"apply_url,omitempty"`
	Location           string    `json:"location"`
	Department         string    `json:"department"`
	Seniority          string    `json:"seniority"`
	Description        string    `json:"description,omitempty"`
	Keywords           []string  `json:"keywords"`
	PostedAt           time.Time `json:"posted_at"`
	WorkplaceType      string    `json:"workplace_type,omitempty"`
	RemotePolicy       string    `json:"remote_policy,omitempty"`
	GlobalRemote       bool      `json:"global_remote"`
	ContractorFriendly bool      `json:"contractor_friendly"`
	Compensation       string    `json:"compensation,omitempty"`
	CompanyDomain      string    `json:"company_domain,omitempty"`
	CompanyName        string    `json:"company_name,omitempty"`
	FirstSeenAt        time.Time `json:"first_seen_at"`
	LastSeenAt         time.Time `json:"last_seen_at"`
	IsNew              bool      `json:"is_new"`
}

// CompanyMeta represents discovered company metadata, engineering signals, and active roles.
type CompanyMeta struct {
	Name           string       `json:"name"`
	Domain         string       `json:"domain"`
	CareersURL     string       `json:"careers_url"`
	GitHubOrg      string       `json:"github_org,omitempty"`
	Signals        []string     `json:"signals"`
	OpenRoles      []JobPosting `json:"open_roles"`
	LinkedInURL    string       `json:"linkedin_url,omitempty"`
	HQLocation     string       `json:"hq_location,omitempty"`
	DominantStacks []string     `json:"dominant_stacks,omitempty"`
}

// ATSAdapter abstracts different Applicant Tracking Systems and career scrapers.
type ATSAdapter interface {
	Name() string
	Detect(target string) bool
	Extract(ctx context.Context, target string) (*CompanyMeta, error)
}
