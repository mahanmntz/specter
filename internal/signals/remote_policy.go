package signals

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// RemoteTier classifies how accessible a job is for global candidates and third-world/sanctioned locations.
type RemoteTier int

const (
	// TierGlobalRemote: 🟢 High match. Worldwide, Anywhere, B2B/Contractor (Deel, Remote.com, Crypto, Freelance).
	TierGlobalRemote RemoteTier = 1

	// TierTimezoneFlexible: 🟡 Medium match. Regional or timezone-flexible (e.g. EMEA, LATAM, UTC-4 to UTC+4).
	TierTimezoneFlexible RemoteTier = 2

	// TierGeoRestricted: 🔴 Low match / Incompatible. US/EU legal residency, W-2 only, security clearance.
	TierGeoRestricted RemoteTier = 3
)

// RemotePolicyResult summarizes the remote accessibility and application deep-link of a job posting.
type RemotePolicyResult struct {
	Tier                 RemoteTier `json:"tier"`
	Badge                string     `json:"badge"`
	IsGlobalRemote       bool       `json:"is_global_remote"`
	IsContractorFriendly bool       `json:"is_contractor_friendly"`
	SanctionSafe         bool       `json:"sanction_safe"`
	DirectApplyURL       string     `json:"direct_apply_url"`
	PolicyName           string     `json:"policy_name"`
	SummaryNotes         string     `json:"summary_notes"`
	Scope                Scope      `json:"scope"`
}

var (
	// Restrictive patterns that require domestic tax status or local citizenship
	restrictivePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(authori[sz]ed|eligible|legally entitled|legal right)\s+to\s+work\s+in\s+(the\s+)?(us|u\.s\.|usa|united\s+states)\b`),
		regexp.MustCompile(`(?i)\b(us|u\.s\.)\s+work\s+authori[sz]ation\b`),
		regexp.MustCompile(`(?i)\bmust\s+(be\s+)?(located|based|reside|living|resident)\b[^.\n]{0,40}\b(united\s+states|usa|u\.s\.|canada|united\s+kingdom)\b`),
		regexp.MustCompile(`(?i)\b(itar|export[- ]control(led)?|us\s+persons?)\b`),
		regexp.MustCompile(`(?i)\b(us|u\.s\.|united states)\s+(only|citizenship|citizen|residents|based)\b`),
		regexp.MustCompile(`(?i)\b(must\s+be\s+located\s+in\s+the\s+united\s+states)\b`),
		regexp.MustCompile(`(?i)\b(north\s+america\s+only|us\s+or\s+canada\s+only)\b`),
		regexp.MustCompile(`(?i)\b(security\s+clearance|ts/sci|polygraph)\b`),
		regexp.MustCompile(`(?i)\b(w2\s+only|w-2\s+only|no\s+c2c|no\s+corp-to-corp)\b`),
		regexp.MustCompile(`(?i)\b(uk\s+only|eu\s+only|germany\s+only|canada\s+only)\b`),
		regexp.MustCompile(`(?i)\b(authorized\s+to\s+work\s+in\s+the\s+us\s+without\s+sponsorship)\b`),
		regexp.MustCompile(`(?i)\b(no\s+contractors|not\s+open\s+to\s+contractors)\b`),
	}
)

// Scope describes who can actually apply to a remote posting.
type Scope string

const (
	ScopeWorldwide Scope = "worldwide"  // anyone, anywhere
	ScopeRegional  Scope = "regional"   // a broad region or timezone band (EMEA, Europe, UTC±3)
	ScopeLocal     Scope = "local"      // tied to named places (Poland, "US or Canada", a city)
	ScopeRestrict  Scope = "restricted" // explicit legal restriction (US-only, clearance, W-2)
	ScopeUnknown   Scope = "unknown"    // says "remote" but never says where
	ScopeOnsite    Scope = "onsite"     // an office location
)

// TierUnknown: the posting is remote but does not say who may apply.
const TierUnknown RemoteTier = 4

var (
	// Words that carry no geographic meaning inside a location string.
	locationNoise = regexp.MustCompile(`(?i)\b(remote(ly)?|hybrid|on-?site|fully|work from home|wfh|home[- ]based|distributed|flexible|virtual|based|within|in|or|and|the|only|from|candidates?|located|anywhere|worldwide|global|globally)\b`)
	nonLetters    = regexp.MustCompile(`[^\p{L}]+`)

	worldwideLocation = regexp.MustCompile(`(?i)\b(anywhere|worldwide|world[- ]wide|global(ly)?|earth|any country)\b`)
	worldwideText     = regexp.MustCompile(`(?i)(work from anywhere|anywhere in the world|from anywhere|hiring (globally|worldwide|across the world)|worldwide|global remote|fully distributed.{0,40}(all|any) time ?zones?|any time ?zone)`)
	regionalLocation  = regexp.MustCompile(`(?i)\b(emea|europe|european|eu|eea|apac|asia|asean|latam|latin america|south america|middle east|mena|africa|americas|cet|cest|gmt|utc\s?[+-]\s?\d+|(european|eastern|central|pacific|mountain) time)\b`)
	contractorText    = regexp.MustCompile(`(?i)\b(independent contractors?|contractors?|b2b|freelanc\w*|deel|remote\.com|oyster(hr)?|invoice|1099|c2c)\b`)
	noContractorText  = regexp.MustCompile(`(?i)(no contractors|not open to contractors|employees only|w-?2 only|no c2c|no corp-to-corp|no third[- ]party)`)
)

// placeOf returns the geographic words left in a location once remote-work wording is removed
// ("Remote - Canada" → "Canada"; "Remote" → "").
func placeOf(location string) string {
	cleaned := locationNoise.ReplaceAllString(location, " ")
	cleaned = nonLetters.ReplaceAllString(cleaned, " ")
	return strings.Join(strings.Fields(cleaned), " ")
}

func result(tier RemoteTier, scope Scope, badge, name, notes string, global, contractor, sanctionSafe bool, apply string) RemotePolicyResult {
	return RemotePolicyResult{
		Tier: tier, Badge: badge, IsGlobalRemote: global, IsContractorFriendly: contractor,
		SanctionSafe: sanctionSafe, DirectApplyURL: apply, PolicyName: name, SummaryNotes: notes, Scope: scope,
	}
}

// ClassifyRemotePolicy decides who can apply to a posting from its location text, then its description.
// It prefers "unknown" over guessing "worldwide": a posting is only worldwide when it says so.
func ClassifyRemotePolicy(location, description, postingURL string) RemotePolicyResult {
	apply := GenerateDirectApplyURL(postingURL)
	locLower := strings.ToLower(location)
	combined := location + " " + description
	contractor := contractorText.MatchString(combined) && !noContractorText.MatchString(combined)
	isRemote := strings.Contains(locLower, "remote") || strings.Contains(locLower, "anywhere") || strings.Contains(locLower, "work from home")
	place := placeOf(location)

	// 1. Explicit legal restrictions always win.
	for _, re := range restrictivePatterns {
		if re.MatchString(combined) {
			return result(TierGeoRestricted, ScopeRestrict, "🔴 Geo-restricted (domestic / W-2)", "Geo-Restricted",
				"Requires domestic legal residency, W-2 status or a clearance.", false, false, false, apply)
		}
	}

	// 2. The location field is the most reliable statement of who can apply.
	if worldwideLocation.MatchString(location) && place == "" {
		return result(TierGlobalRemote, ScopeWorldwide, "🟢 Worldwide", "Worldwide",
			"The posting lists a worldwide location.", true, contractor, true, apply)
	}
	if place != "" && isRemote {
		if regionalLocation.MatchString(location) {
			return result(TierTimezoneFlexible, ScopeRegional, "🟡 Remote · "+strings.ToUpper(place), "Regional",
				"Remote within a broad region or timezone band.", false, contractor, true, apply)
		}
		return result(TierGeoRestricted, ScopeLocal, "📍 Remote · "+titleCase(place), "Local",
			"Remote, but tied to the places listed; check you are eligible.", false, contractor, false, apply)
	}
	if place != "" && !isRemote {
		return result(TierTimezoneFlexible, ScopeOnsite, "📍 "+location, location,
			fmt.Sprintf("Primary location listed as %s; verify remote contract availability.", location), false, contractor, false, apply)
	}

	// 3. "Remote" with no place (or no location at all): read the description.
	if isRemote || strings.TrimSpace(location) == "" {
		switch {
		case worldwideText.MatchString(description):
			return result(TierGlobalRemote, ScopeWorldwide, "🟢 Worldwide", "Worldwide",
				"The description says the role can be done from anywhere.", true, contractor, true, apply)
		case regionalLocation.MatchString(description):
			return result(TierTimezoneFlexible, ScopeRegional, "🟡 Remote · regional", "Regional",
				"Remote with regional or timezone expectations.", false, contractor, true, apply)
		case isRemote:
			return result(TierUnknown, ScopeUnknown, "⚪ Remote · region not stated", "Remote",
				"Remote, but the posting does not say where candidates may live.", false, contractor, false, apply)
		}
	}

	return result(TierTimezoneFlexible, ScopeOnsite, "📍 Location not stated", location,
		"No remote information found.", false, contractor, false, apply)
}

// StructuredPolicy builds a policy from a feed that states eligibility explicitly
// (for example a `locationRestrictions` list), so no guessing is involved.
func StructuredPolicy(scope Scope, places []string, contractor bool, postingURL string) RemotePolicyResult {
	apply := GenerateDirectApplyURL(postingURL)
	list := strings.Join(places, ", ")
	switch scope {
	case ScopeWorldwide:
		return result(TierGlobalRemote, ScopeWorldwide, "🟢 Worldwide", "Worldwide", "The source lists no location restriction.", true, contractor, true, apply)
	case ScopeRegional:
		return result(TierTimezoneFlexible, ScopeRegional, "🟡 Remote · "+list, "Regional", "Remote within the listed regions.", false, contractor, true, apply)
	case ScopeLocal, ScopeRestrict:
		return result(TierGeoRestricted, ScopeLocal, "📍 Remote · "+list, "Local", "Remote, but limited to the listed places.", false, contractor, false, apply)
	default:
		return result(TierUnknown, ScopeUnknown, "⚪ Remote · region not stated", "Remote", "The source does not state eligibility.", false, contractor, false, apply)
	}
}

func titleCase(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if len(w) <= 3 && (w == "us" || w == "uk" || w == "usa" || w == "uae") {
			words[i] = strings.ToUpper(w)
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// GenerateDirectApplyURL transforms an ATS job posting link into a direct application deep link.
func GenerateDirectApplyURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	cleanURL := strings.TrimSpace(rawURL)
	u, err := url.Parse(cleanURL)
	if err != nil {
		return cleanURL
	}

	host := strings.ToLower(u.Hostname())
	path := strings.TrimRight(u.Path, "/")

	// Greenhouse: append #app to jump directly to application fields
	if strings.Contains(host, "greenhouse.io") {
		if strings.Contains(path, "/jobs/") {
			return fmt.Sprintf("https://%s%s#app", host, path)
		}
		return cleanURL
	}

	// Lever: append /apply
	if strings.Contains(host, "lever.co") {
		if !strings.HasSuffix(path, "/apply") {
			return fmt.Sprintf("https://%s%s/apply", host, path)
		}
		return cleanURL
	}

	// Ashby: append /application
	if strings.Contains(host, "ashbyhq.com") {
		if !strings.HasSuffix(path, "/application") {
			return fmt.Sprintf("https://%s%s/application", host, path)
		}
		return cleanURL
	}

	return cleanURL
}

// GenerateQuickPitch crafts a concise, 3-sentence application pitch note tailored for global remote backend engineers.
func GenerateQuickPitch(roleTitle, companyName, topTech string) string {
	if topTech == "" {
		topTech = "Go & Distributed Systems"
	}
	if companyName == "" {
		companyName = "your team"
	}

	return fmt.Sprintf(
		"Hi %s team,\n\n"+
			"I am applying for the %s position. I specialize in %s with extensive experience building high-throughput, fault-tolerant backend services in fully distributed, async environments.\n\n"+
			"I work seamlessly under contractor/B2B agreements (via Deel/direct invoicing) across global timezones and take full ownership of backend reliability and feature delivery.\n\n"+
			"Looking forward to discussing how I can contribute to %s's infrastructure.\n\n"+
			"Best regards,\n[Your Name] | [Your GitHub/Portfolio]",
		companyName, roleTitle, topTech, companyName,
	)
}
