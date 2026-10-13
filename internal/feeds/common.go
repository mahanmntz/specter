package feeds

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/mahanmntz/specter/internal/signals"
)

var (
	htmlTags  = regexp.MustCompile(`<[^>]+>`)
	htmlBreak = regexp.MustCompile(`(?i)<(br|/p|/li|/div|/h\d)\s*/?>`)
	spaces    = regexp.MustCompile(`[ \t\r\f\v]+`)
)

// stripHTML turns an HTML fragment into plain text.
func stripHTML(s string) string {
	s = htmlBreak.ReplaceAllString(s, "\n")
	s = htmlTags.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = spaces.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// broadRegions are labels that name many countries at once rather than one place.
var broadRegions = map[string]bool{
	"europe": true, "emea": true, "eu": true, "apac": true, "asia": true, "latam": true, "latin america": true,
	"south america": true, "middle east": true, "mena": true, "africa": true, "americas": true, "oceania": true,
}

func isWorldwide(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	return l == "anywhere" || l == "worldwide" || l == "global" || l == "world" || l == "remote" || l == "anywhere in the world"
}

// geoPolicy turns a source's own location label ("Anywhere", "Europe", "USA", "USA, Canada")
// into an eligibility policy. Nothing is guessed: unlabelled postings stay "unknown".
func geoPolicy(geo string, contractor bool, postingURL string) signals.RemotePolicyResult {
	var parts []string
	for _, p := range strings.FieldsFunc(geo, func(r rune) bool { return r == ',' || r == ';' || r == '/' }) {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return signals.StructuredPolicy(signals.ScopeUnknown, nil, contractor, postingURL)
	}
	for _, p := range parts {
		if isWorldwide(p) {
			return signals.StructuredPolicy(signals.ScopeWorldwide, nil, contractor, postingURL)
		}
	}
	allBroad := true
	for _, p := range parts {
		if !broadRegions[strings.ToLower(p)] {
			allBroad = false
		}
	}
	if allBroad {
		return signals.StructuredPolicy(signals.ScopeRegional, parts, contractor, postingURL)
	}
	return signals.StructuredPolicy(signals.ScopeLocal, summarizePlaces(parts), contractor, postingURL)
}

// summarizePlaces shortens long country lists for display ("Canada, UK, Ireland +12").
func summarizePlaces(places []string) []string {
	if len(places) <= 3 {
		return places
	}
	return append(append([]string{}, places[:3]...), fmt.Sprintf("+%d", len(places)-3))
}

func unixTime(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}
