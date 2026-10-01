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
}

var (
	// Restrictive patterns that require domestic tax status or local citizenship
	restrictivePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(us|u\.s\.|united states)\s+(only|citizenship|citizen|residents|based)\b`),
		regexp.MustCompile(`(?i)\b(must\s+be\s+located\s+in\s+the\s+united\s+states)\b`),
		regexp.MustCompile(`(?i)\b(north\s+america\s+only|us\s+or\s+canada\s+only)\b`),
		regexp.MustCompile(`(?i)\b(security\s+clearance|ts/sci|polygraph)\b`),
		regexp.MustCompile(`(?i)\b(w2\s+only|w-2\s+only|no\s+c2c|no\s+corp-to-corp)\b`),
		regexp.MustCompile(`(?i)\b(uk\s+only|eu\s+only|germany\s+only|canada\s+only)\b`),
		regexp.MustCompile(`(?i)\b(authorized\s+to\s+work\s+in\s+the\s+us\s+without\s+sponsorship)\b`),
		regexp.MustCompile(`(?i)\b(no\s+contractors|not\s+open\s+to\s+contractors)\b`),
	}

	// Global / contractor / B2B friendly indicators
	globalFriendlyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(anywhere|worldwide|work\s+from\s+anywhere|global\s+remote|fully\s+distributed)\b`),
		regexp.MustCompile(`(?i)\b(contractor|independent\s+contractor|b2b|contract-to-hire|freelance)\b`),
		regexp.MustCompile(`(?i)\b(deel|remote\.com|oysterhr|invoice|crypto|usdt|usdc)\b`),
		regexp.MustCompile(`(?i)\b(async-first|asynchronous|all\s+timezones|timezone\s+agnostic)\b`),
		regexp.MustCompile(`(?i)\b(open\s+source\s+first|global\s+team|hiring\s+globally)\b`),
	}

	// Regional / timezone friendly (EMEA, UTC±4)
	regionalPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(emea|europe|latam|apac|utc[+-][0-9]|cest|gmt)\b`),
		regexp.MustCompile(`(?i)\b(4\s+hours\s+overlap|overlap\s+with\s+utc)\b`),
	}
)

// ClassifyRemotePolicy evaluates a job posting's location, description, and apply URL to assess remote friendliness.
func ClassifyRemotePolicy(location, description, postingURL string) RemotePolicyResult {
	locLower := strings.ToLower(location)
	combined := strings.ToLower(location + " " + description)

	directApply := GenerateDirectApplyURL(postingURL)

	// 1. Direct check on location for immediate Worldwide / Global indicator
	isWorldwideLoc := strings.Contains(locLower, "anywhere") ||
		strings.Contains(locLower, "worldwide") ||
		strings.Contains(locLower, "global") ||
		strings.Contains(locLower, "remote - global") ||
		strings.Contains(locLower, "remote (worldwide)") ||
		strings.Contains(locLower, "remote / anywhere")

	// 2. Check for hard restrictions
	for _, re := range restrictivePatterns {
		if re.MatchString(combined) {
			return RemotePolicyResult{
				Tier:                 TierGeoRestricted,
				Badge:                "🔴 Geo-Restricted (Domestic / W-2)",
				IsGlobalRemote:       false,
				IsContractorFriendly: false,
				SanctionSafe:         false,
				DirectApplyURL:       directApply,
				PolicyName:           "Geo-Restricted",
				SummaryNotes:         "Requires domestic US/EU legal residency or W2 tax authorization.",
			}
		}
	}

	// 3. Check for Global / Contractor signals
	globalMatches := 0
	for _, re := range globalFriendlyPatterns {
		if re.MatchString(combined) {
			globalMatches++
		}
	}

	isContractor := strings.Contains(combined, "contractor") ||
		strings.Contains(combined, "b2b") ||
		strings.Contains(combined, "deel") ||
		strings.Contains(combined, "remote.com") ||
		strings.Contains(combined, "independent contractor")

	// Check if location or description is explicitly regional/timezone-bound (and not worldwide)
	isRegional := false
	for _, re := range regionalPatterns {
		if re.MatchString(locLower) || re.MatchString(combined) {
			isRegional = true
			break
		}
	}

	if !isWorldwideLoc && isRegional {
		return RemotePolicyResult{
			Tier:                 TierTimezoneFlexible,
			Badge:                "🟡 Timezone-Flexible (EMEA/Global)",
			IsGlobalRemote:       true,
			IsContractorFriendly: isContractor,
			SanctionSafe:         true,
			DirectApplyURL:       directApply,
			PolicyName:           "Regional / Timezone Flexible",
			SummaryNotes:         "Remote with timezone overlap expectations (EMEA/UTC friendly).",
		}
	}

	if isWorldwideLoc || globalMatches >= 2 || (globalMatches >= 1 && strings.Contains(locLower, "remote")) {
		return RemotePolicyResult{
			Tier:                 TierGlobalRemote,
			Badge:                "🟢 Worldwide / Contractor-Friendly",
			IsGlobalRemote:       true,
			IsContractorFriendly: isContractor || true, // Worldwide devtools startups standardly use Deel/Contractor
			SanctionSafe:         true,
			DirectApplyURL:       directApply,
			PolicyName:           "Global Remote (Anywhere)",
			SummaryNotes:         "Worldwide remote friendly; accessible via B2B/Contractor or Deel.",
		}
	}

	// 5. Default generic Remote
	if strings.Contains(locLower, "remote") {
		return RemotePolicyResult{
			Tier:                 TierGlobalRemote,
			Badge:                "🟢 Remote (No Geo-Block Detected)",
			IsGlobalRemote:       true,
			IsContractorFriendly: isContractor,
			SanctionSafe:         true,
			DirectApplyURL:       directApply,
			PolicyName:           "Remote",
			SummaryNotes:         "Open remote requisition without explicit domestic restrictions.",
		}
	}

	// Onsite or location-bound fallback
	return RemotePolicyResult{
		Tier:                 TierTimezoneFlexible,
		Badge:                "🟡 Location Specified",
		IsGlobalRemote:       false,
		IsContractorFriendly: isContractor,
		SanctionSafe:         false,
		DirectApplyURL:       directApply,
		PolicyName:           location,
		SummaryNotes:         fmt.Sprintf("Primary location listed as %s; verify remote contract availability.", location),
	}
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
