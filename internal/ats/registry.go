package ats

import (
	"context"
	"strings"

	"github.com/mahanmntz/specter/internal/crawler"
	"github.com/mahanmntz/specter/internal/seeds"
)

// Registry manages and dispatches to ATS adapters.
type Registry struct {
	adapters []ATSAdapter
	fallback ATSAdapter
	boards   map[string]BoardAdapter
}

// NewRegistry initializes all ATS adapters with the provided fetcher.
func NewRegistry(f *crawler.Fetcher) *Registry {
	boards := map[string]BoardAdapter{
		"greenhouse":      NewGreenhouseAdapter(f),
		"lever":           NewLeverAdapter(f),
		"ashby":           NewAshbyAdapter(f),
		"workable":        NewWorkableAdapter(f),
		"smartrecruiters": NewSmartRecruitersAdapter(f),
		"recruitee":       NewRecruiteeAdapter(f),
	}

	// Detection order matters only for overlapping hostnames; keep it deterministic.
	order := []string{"greenhouse", "lever", "ashby", "workable", "smartrecruiters", "recruitee"}
	adapters := make([]ATSAdapter, 0, len(order))
	for _, name := range order {
		adapters = append(adapters, boards[name])
	}

	return &Registry{adapters: adapters, fallback: NewGenericAdapter(f), boards: boards}
}

// ResolveAndExtract finds the best matching adapter for the target and executes extraction.
func (r *Registry) ResolveAndExtract(ctx context.Context, target string) (ATSAdapter, *CompanyMeta, error) {
	for _, a := range r.adapters {
		if a.Detect(target) {
			meta, err := a.Extract(ctx, target)
			return a, meta, err
		}
	}
	meta, err := r.fallback.Extract(ctx, target)
	return r.fallback, meta, err
}

// ExtractBySeed extracts company roles using the direct API corresponding to the seed's ATS service and board ID.
func (r *Registry) ExtractBySeed(ctx context.Context, seed seeds.SeedTarget) (*CompanyMeta, error) {
	var (
		meta *CompanyMeta
		err  error
	)
	if board, ok := r.boards[strings.ToLower(seed.ATSService)]; ok {
		meta, err = board.ExtractByBoardID(ctx, seed.ATSBoardID, seed.Domain)
	} else {
		target := seed.Domain
		if !strings.HasPrefix(target, "http") {
			target = "https://" + target
		}
		meta, err = r.fallback.Extract(ctx, target)
	}
	if err == nil && meta != nil {
		meta.Name = seed.Name
		meta.Domain = seed.Domain
		meta.GitHubOrg = seed.GitHubOrg
	}
	return meta, err
}
