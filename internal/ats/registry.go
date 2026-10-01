package ats

import (
	"context"
	"strings"

	"specter/internal/crawler"
	"specter/internal/seeds"
)

// Registry manages and dispatches to ATS adapters.
type Registry struct {
	adapters   []ATSAdapter
	fallback   ATSAdapter
	greenhouse *GreenhouseAdapter
	lever      *LeverAdapter
	ashby      *AshbyAdapter
}

// NewRegistry initializes all ATS adapters with the provided fetcher.
func NewRegistry(f *crawler.Fetcher) *Registry {
	gh := NewGreenhouseAdapter(f)
	lv := NewLeverAdapter(f)
	as := NewAshbyAdapter(f)
	gen := NewGenericAdapter(f)

	return &Registry{
		adapters: []ATSAdapter{
			gh,
			lv,
			as,
		},
		fallback:   gen,
		greenhouse: gh,
		lever:      lv,
		ashby:      as,
	}
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
	switch strings.ToLower(seed.ATSService) {
	case "greenhouse":
		meta, err := r.greenhouse.ExtractByBoardID(ctx, seed.ATSBoardID, seed.Domain)
		if err == nil && meta != nil {
			meta.Name = seed.Name
			meta.Domain = seed.Domain
			meta.GitHubOrg = seed.GitHubOrg
			return meta, nil
		}
		return meta, err
	case "lever":
		meta, err := r.lever.ExtractByBoardID(ctx, seed.ATSBoardID, seed.Domain)
		if err == nil && meta != nil {
			meta.Name = seed.Name
			meta.Domain = seed.Domain
			meta.GitHubOrg = seed.GitHubOrg
			return meta, nil
		}
		return meta, err
	case "ashby":
		meta, err := r.ashby.ExtractByBoardID(ctx, seed.ATSBoardID, seed.Domain)
		if err == nil && meta != nil {
			meta.Name = seed.Name
			meta.Domain = seed.Domain
			meta.GitHubOrg = seed.GitHubOrg
			return meta, nil
		}
		return meta, err
	default:
		target := seed.Domain
		if !strings.HasPrefix(target, "http") {
			target = "https://" + target
		}
		meta, err := r.fallback.Extract(ctx, target)
		if err == nil && meta != nil {
			meta.Name = seed.Name
			meta.Domain = seed.Domain
			meta.GitHubOrg = seed.GitHubOrg
			return meta, nil
		}
		return meta, err
	}
}
