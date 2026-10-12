package signals

import (
	"strings"
)

// IsBotOrCI checks author name, GitHub handle, and email against strict bot and CI/CD rules.
func IsBotOrCI(name, handle, email string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	h := strings.ToLower(strings.TrimSpace(handle))
	e := strings.ToLower(strings.TrimSpace(email))

	// 1. Email checks
	if e != "" {
		if strings.Contains(e, "noreply.github.com") ||
			strings.Contains(e, "teamcity") ||
			strings.Contains(e, "actions@") ||
			strings.Contains(e, "jenkins") ||
			strings.Contains(e, "circleci") ||
			strings.Contains(e, "buildkite") ||
			strings.Contains(e, "dependabot") ||
			strings.Contains(e, "sentry") ||
			strings.Contains(e, "[bot]") ||
			strings.Contains(e, "bot@") ||
			strings.Contains(e, "automation") ||
			strings.Contains(e, "greenkeeper") ||
			strings.Contains(e, "renovate") ||
			strings.Contains(e, "snyk") ||
			strings.Contains(e, "codecov") {
			return true
		}
	}

	// 2. Name & Handle checks
	isBotString := func(s string) bool {
		if s == "" {
			return false
		}
		if strings.Contains(s, "[bot]") ||
			strings.Contains(s, "automation") ||
			strings.Contains(s, "teamcity") ||
			strings.Contains(s, "jenkins") ||
			strings.Contains(s, "circleci") ||
			strings.Contains(s, "buildkite") ||
			strings.Contains(s, "dependabot") ||
			strings.Contains(s, "sentry") ||
			strings.Contains(s, "github-actions") ||
			strings.Contains(s, "codecov") ||
			strings.HasSuffix(s, "-bot") ||
			strings.HasSuffix(s, "_bot") ||
			strings.HasPrefix(s, "bot-") ||
			strings.HasPrefix(s, "bot_") {
			return true
		}

		tokens := strings.FieldsFunc(s, func(r rune) bool {
			return r == ' ' || r == '-' || r == '_' || r == '.' || r == '/' || r == '[' || r == ']' || r == '(' || r == ')'
		})
		for _, tok := range tokens {
			if tok == "bot" || tok == "ci" || tok == "cd" || tok == "automation" || (strings.HasSuffix(tok, "bot") && len(tok) > 3 && !strings.Contains(tok, "abbott")) {
				return true
			}
		}
		return false
	}

	if isBotString(n) || isBotString(h) {
		return true
	}

	return false
}

// IsValidOutreachEmail validates that an email is legitimate and excludes bots and generic inboxes.
func IsValidOutreachEmail(email string) bool {
	if email == "" {
		return false
	}
	email = strings.TrimSpace(strings.ToLower(email))

	if !emailRegex.MatchString(email) {
		return false
	}

	// Bot & CI noise
	if strings.Contains(email, "users.noreply.github.com") ||
		strings.Contains(email, "noreply.github.com") ||
		strings.Contains(email, "teamcity") ||
		strings.Contains(email, "actions@") ||
		strings.Contains(email, "jenkins") ||
		strings.Contains(email, "circleci") ||
		strings.Contains(email, "buildkite") ||
		strings.Contains(email, "dependabot") ||
		strings.Contains(email, "sentry") ||
		strings.Contains(email, "github-actions") ||
		strings.Contains(email, "greenkeeper") ||
		strings.Contains(email, "snyk") ||
		strings.Contains(email, "renovate") ||
		strings.Contains(email, "bot@") ||
		strings.Contains(email, "[bot]") ||
		strings.Contains(email, "codecov") {
		return false
	}

	// Common test/dummy domains
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}
	userPart, domainPart := parts[0], parts[1]

	if domainPart == "example.com" || domainPart == "localhost" || domainPart == "test.com" {
		return false
	}

	// Generic company mailboxes
	if genericPrefixes[userPart] {
		return false
	}

	return true
}
