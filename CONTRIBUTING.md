# Contributing to Specter

Thanks for your interest! Specter is a Go CLI; contributions of all sizes are welcome.

## Getting started

```bash
git clone https://github.com/mahanmntz/specter.git
cd specter
cp .env.example .env     # optional; add GitHub tokens for higher rate limits
go build ./...
go test ./...
```

Requires Go 1.26+.

## Before opening a PR

- `gofmt -l .` prints nothing
- `go vet ./...` is clean
- `go test -race ./...` passes
- New behavior has tests; new job sources (ATS adapters) include a fixture-based test
- Keep PRs focused: one feature or fix per PR

## Good first contributions

- A new ATS adapter in `internal/ats/` (Workable, SmartRecruiters, Recruitee, ...)
- New seed companies in `internal/seeds/seeds.json`
- Output integrations (webhooks, Notion, Airtable)
- Docs and examples

## Ground rules

- Respect robots.txt, site terms, and rate limits. Do not add features designed to evade them.
- Do not commit real personal data (emails, reports, tokens, `.env`, `*.db`).
- See [SECURITY.md](SECURITY.md) and the ethical-use section of the README.

By contributing you agree your work is released under the MIT License.
