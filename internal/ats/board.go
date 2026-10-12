package ats

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"specter/internal/signals"
)

// BoardAdapter is an ATS adapter that can be queried directly with a board/company ID.
type BoardAdapter interface {
	ATSAdapter
	ExtractByBoardID(ctx context.Context, boardID, domain string) (*CompanyMeta, error)
}

// RawJob is the normalized shape every ATS adapter maps its postings into
// before shared relevance filtering and remote-policy classification.
type RawJob struct {
	ID          string
	Title       string
	Department  string
	Location    string
	Workplace   string // optional hint, e.g. "remote"
	URL         string
	Description string
	PostedAt    time.Time
}

// BuildMeta filters raw postings down to relevant backend roles and assembles CompanyMeta.
// idPrefix namespaces role IDs per ATS (e.g. "workable").
func BuildMeta(name, domain, careersURL, idPrefix string, jobs []RawJob) *CompanyMeta {
	meta := &CompanyMeta{
		Name:       name,
		Domain:     domain,
		CareersURL: careersURL,
		Signals:    []string{},
		OpenRoles:  []JobPosting{},
	}
	signalSet := make(map[string]bool)

	for _, j := range jobs {
		if !signals.IsBackendRole(j.Title, j.Department) {
			continue
		}
		combined := j.Title + " " + j.Department + " " + j.Description
		kws := signals.MatchBackendKeywords(combined)
		if len(kws) == 0 {
			continue
		}
		for _, kw := range kws {
			signalSet[kw] = true
		}
		posted := j.PostedAt
		if posted.IsZero() {
			posted = time.Now().UTC()
		}
		policy := signals.ClassifyRemotePolicy(j.Location+" "+j.Workplace, combined, j.URL)
		meta.OpenRoles = append(meta.OpenRoles, JobPosting{
			ID:                 fmt.Sprintf("%s-%s", idPrefix, j.ID),
			Title:              j.Title,
			URL:                j.URL,
			ApplyURL:           policy.DirectApplyURL,
			Location:           j.Location,
			Department:         j.Department,
			Seniority:          signals.ExtractSeniority(j.Title),
			Keywords:           kws,
			PostedAt:           posted,
			WorkplaceType:      policy.PolicyName,
			RemotePolicy:       policy.Badge,
			GlobalRemote:       policy.IsGlobalRemote,
			ContractorFriendly: policy.IsContractorFriendly,
		})
	}
	for kw := range signalSet {
		meta.Signals = append(meta.Signals, kw)
	}
	sort.Strings(meta.Signals)
	return meta
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// lastPathSegment returns the first path segment after the host for URLs like
// https://apply.workable.com/<account>/..., or the bare value when target is not a URL.
func lastPathSegment(target, hostHint string) string {
	if !strings.Contains(target, "://") {
		if strings.Contains(target, hostHint) {
			target = "https://" + target
		} else {
			return strings.Trim(target, "/")
		}
	}
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}
