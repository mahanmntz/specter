package ghprofile

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mahanmntz/specter/internal/signals"
)

// languageSkills maps GitHub's language labels onto catalog skills (beyond exact-name matches).
var languageSkills = map[string][]string{
	"Jupyter Notebook": {"Python", "Machine Learning"},
	"HCL":              {"Terraform"},
	"Dockerfile":       {"Docker"},
	"PLpgSQL":          {"PostgreSQL", "SQL"},
	"TSQL":             {"SQL"},
	"SCSS":             {"CSS"},
	"Objective-C":      {"iOS"},
	"Vue":              {"Vue"},
	"Svelte":           {"Svelte"},
	"Smarty":           {"Helm"},
	"Solidity":         {"Solidity", "Smart Contracts"},
	"ShaderLab":        {"Unity"},
	"Less":             {"CSS"},
	"Sass":             {"CSS"},
	"Bash":             {"Shell"},
	"PowerShell":       {"Shell"},
	"Objective-C++":    {"iOS"},
	"Cuda":             {"C++"},
	"Verilog":          {"FPGA"},
	"VHDL":             {"FPGA"},
	"Assembly":         {"Assembly"},
	"Jsonnet":          {"Kubernetes"},
}

// genericSkills show up in almost every repository, so they only count when clearly emphasised.
var genericSkills = map[string]bool{"HTML": true, "CSS": true, "Shell": true, "Git": true, "Testing": true, "Security": true, "REST": true, "Linux": true}

// throwawayRepo flags learning/demo repositories, which say little about professional skill.
var throwawayRepo = regexp.MustCompile(`(?i)(tutorial|learn|course|bootcamp|exercise|playground|sandbox|hello[-_ ]?world|dotfiles|cheat ?sheet|leetcode|kata|homework|assignment|todo[-_ ]?app|test[-_]?repo|boilerplate|starter)`)

const (
	recencyHalfLifeMonths = 18.0
	selfDeclaredBoost     = 2.5
	maxPreselected        = 8
	maxAlsoFound          = 12
)

// repoWeight is how much one repository says about the owner's current abilities:
// recent, sustained, starred and substantial original work counts the most.
func repoWeight(r ghRepo, now time.Time) float64 {
	months := now.Sub(r.PushedAt).Hours() / (24 * 30)
	if months < 0 {
		months = 0
	}
	w := 0.6 + 2.4*math.Pow(0.5, months/recencyHalfLifeMonths)
	w += math.Min(2, 0.4*math.Log2(1+float64(r.Stars)))
	if !r.CreatedAt.IsZero() && r.PushedAt.After(r.CreatedAt) {
		span := r.PushedAt.Sub(r.CreatedAt).Hours() / (24 * 30)
		w += 0.6 * math.Min(1, span/12) // sustained effort, not a one-day commit
	}
	w += math.Min(1, 0.25*math.Log2(1+float64(r.Size)/200))
	if throwawayRepo.MatchString(r.Name + " " + r.Description) {
		w *= 0.4
	}
	if r.Archived {
		w *= 0.6
	}
	return w
}

// profileText is what the user wrote about themselves.
type profileText struct {
	skills []string
	counts map[string]int     // skill -> how often it is mentioned
	hints  map[string]float64 // role id -> self-declared strength
}

var learningLine = regexp.MustCompile(`(?i)\b(learning|learn |exploring|interested in|want to|wanna|planning|studying|curious|beginner|currently reading)\b`)

// roleHints maps phrases people use to describe their job onto presets.
var roleHints = []struct {
	role string
	re   *regexp.Regexp
}{
	{"backend", regexp.MustCompile(`(?i)\bback[- ]?end\b`)},
	{"fullstack", regexp.MustCompile(`(?i)\bfull[- ]?stack\b`)},
	{"frontend", regexp.MustCompile(`(?i)\bfront[- ]?end\b|\bui engineer\b|\bweb designer\b`)},
	{"devops", regexp.MustCompile(`(?i)\bdevops\b|\bsre\b|site reliability|platform engineer|infrastructure engineer|cloud engineer`)},
	{"mobile", regexp.MustCompile(`(?i)\bmobile (developer|engineer|dev)\b|\bios (developer|engineer|dev)\b|\bandroid (developer|engineer|dev)\b|\bflutter developer\b`)},
	{"data", regexp.MustCompile(`(?i)data engineer|analytics engineer|data engineering|data platform`)},
	{"ml", regexp.MustCompile(`(?i)machine learning|\bml engineer\b|\bai engineer\b|deep learning|\bmlops\b|\bllm\b`)},
	{"security", regexp.MustCompile(`(?i)security (engineer|researcher)|\bappsec\b|pentest|penetration test|bug bounty|ethical hack`)},
	{"qa", regexp.MustCompile(`(?i)\bqa\b|\bsdet\b|test automation|quality assurance`)},
	{"embedded", regexp.MustCompile(`(?i)\bembedded\b|\bfirmware\b|\brtos\b`)},
	{"blockchain", regexp.MustCompile(`(?i)blockchain|\bweb3\b|smart contract|\bsolidity\b|\bdefi\b`)},
	{"game", regexp.MustCompile(`(?i)game (dev|develop|programmer)|\bgamedev\b|unity developer`)},
	{"software-engineer", regexp.MustCompile(`(?i)software (engineer|developer)|\bswe\b|\bprogrammer\b`)},
}

// readProfileText extracts skills and role hints from the bio and profile README, ignoring
// lines about things the person is still learning.
func readProfileText(bio, readme string) profileText {
	pt := profileText{hints: map[string]float64{}}
	var kept []string
	for _, line := range strings.Split(bio+"\n"+readme, "\n") {
		if learningLine.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	text := strings.Join(kept, "\n")
	pt.counts = signals.CountSkills(text)
	for skill := range pt.counts {
		pt.skills = append(pt.skills, skill)
	}
	sort.Strings(pt.skills)
	for _, h := range roleHints {
		if h.re.MatchString(text) {
			pt.hints[h.role] = 3
		}
	}
	return pt
}

type skillAcc struct {
	weight   float64
	repos    int
	lastUsed time.Time
	profile  bool
}

// weighSkills turns repositories plus self-description into per-skill evidence.
func weighSkills(repos []ghRepo, langBytes map[string]map[string]int, pt profileText, now time.Time) (acc map[string]*skillAcc, analyzed int) {
	acc = map[string]*skillAcc{}
	originals := 0
	for _, r := range repos {
		if !r.Fork {
			originals++
		}
	}
	for _, r := range repos {
		// Forks are mostly other people's code; ignore them unless that is all there is.
		if r.Fork && originals > 0 {
			continue
		}
		analyzed++
		w := repoWeight(r, now)

		hit := map[string]float64{}
		add := func(skill string, factor float64) {
			if v := w * factor; v > hit[skill] {
				hit[skill] = v
			}
		}
		if bytes := langBytes[r.Name]; len(bytes) > 0 {
			// Real byte counts: each language counts in proportion to its share of the code.
			total := 0
			for _, b := range bytes {
				total += b
			}
			for lang, b := range bytes {
				share := float64(b) / float64(total)
				if share < 0.05 {
					continue
				}
				factor := math.Min(1, math.Max(0.25, share/0.5))
				if canon, ok := signals.CanonicalSkill(lang); ok {
					add(canon, factor)
				}
				for _, s := range languageSkills[lang] {
					add(s, factor)
				}
			}
		} else if r.Language != "" {
			if canon, ok := signals.CanonicalSkill(r.Language); ok {
				add(canon, 1)
			}
			for _, s := range languageSkills[r.Language] {
				add(s, 1)
			}
		}
		for _, topic := range r.Topics {
			if canon, ok := signals.CanonicalSkill(topic); ok {
				add(canon, 1.5)
			}
		}
		for _, s := range signals.FindSkills(r.Description + " " + strings.ReplaceAll(r.Name, "-", " ")) {
			add(s, 0.5)
		}
		for skill, v := range hit {
			a := acc[skill]
			if a == nil {
				a = &skillAcc{}
				acc[skill] = a
			}
			a.weight += v
			a.repos++
			if r.PushedAt.After(a.lastUsed) {
				a.lastUsed = r.PushedAt
			}
		}
	}
	for _, s := range pt.skills {
		a := acc[s]
		if a == nil {
			a = &skillAcc{}
			acc[s] = a
		}
		a.weight += selfDeclaredBoost
		a.profile = true
	}
	return acc, analyzed
}

// selectSkills orders skills by evidence and marks the strong ones for pre-selection.
// "Strong" means substantial relative to the person's best skill and backed by more than one
// repository or by their own words, so one-off experiments are listed but not pre-selected.
func selectSkills(acc map[string]*skillAcc) []DetectedSkill {
	out := make([]DetectedSkill, 0, len(acc))
	top := 0.0
	for name, a := range acc {
		out = append(out, DetectedSkill{Name: name, Score: math.Round(a.weight*10) / 10, Repos: a.repos, LastUsed: a.lastUsed, FromProfile: a.profile})
		top = math.Max(top, a.weight)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Name < out[j].Name
	})

	picked := 0
	for i := range out {
		s := &out[i]
		// Compare on a log scale so one dominant skill does not hide other solid ones.
		minScore := math.Max(2.5, math.Expm1(0.55*math.Log1p(top)))
		if genericSkills[s.Name] {
			minScore = math.Max(minScore, math.Expm1(0.75*math.Log1p(top)))
		}
		backed := s.Repos >= 2 || s.FromProfile || s.Score >= 4
		if picked < maxPreselected && s.Score >= minScore && backed {
			s.Selected = true
			picked++
		}
	}
	if picked == 0 { // sparse profile: still offer a starting point
		for i := range out {
			if i < 3 && out[i].Score >= 1 {
				out[i].Selected = true
			}
		}
	}

	// Keep the long tail short: everything unselected beyond the cap is noise.
	kept := out[:0]
	extra := 0
	for _, s := range out {
		if !s.Selected {
			if extra >= maxAlsoFound {
				continue
			}
			extra++
		}
		kept = append(kept, s)
	}
	return kept
}

// rankRoles scores every preset from skill evidence plus self-declared hints and decides the
// best match. Someone whose work is spread evenly across several roles is a generalist
// "software-engineer" rather than being forced into backend or full-stack.
func rankRoles(acc map[string]*skillAcc, pt profileText) (best string, order []string, scores map[string]float64) {
	weights := make(map[string]float64, len(acc))
	for k, a := range acc {
		weights[k] = a.weight
	}
	code := signals.RoleScores(weights)
	scores = make(map[string]float64, len(code))
	for role, sc := range code {
		scores[role] = sc
		if role != "software-engineer" {
			scores[role] += pt.hints[role]
		}
	}

	type rs struct {
		id    string
		score float64
	}
	var specific []rs
	topCode := 0.0
	for id, sc := range scores {
		if id != "software-engineer" && sc > 0 {
			specific = append(specific, rs{id, sc})
		}
		if id != "software-engineer" && code[id] > topCode {
			topCode = code[id]
		}
	}
	sort.Slice(specific, func(i, j int) bool {
		if specific[i].score != specific[j].score {
			return specific[i].score > specific[j].score
		}
		return specific[i].id < specific[j].id
	})

	best = "software-engineer"
	if len(specific) > 0 {
		// 1. What the person says they are wins, provided their work gives it some support.
		declared, declaredScore := "", 0.0
		for role, h := range pt.hints {
			if role == "software-engineer" || h == 0 {
				continue
			}
			if code[role] >= 0.25*topCode && scores[role] > declaredScore {
				declared, declaredScore = role, scores[role]
			}
		}
		if declared != "" {
			best = declared
		} else {
			// 2. Otherwise work spread evenly over several roles makes a generalist.
			s1 := specific[0].score
			s2, s3 := 0.0, 0.0
			if len(specific) > 1 {
				s2 = specific[1].score
			}
			if len(specific) > 2 {
				s3 = specific[2].score
			}
			broad := s2 >= 0.75*s1 && s3 >= 0.5*s1
			saysGeneralist := pt.hints["software-engineer"] > 0 && s2 >= 0.6*s1
			if !broad && !saysGeneralist {
				best = specific[0].id
			}
		}
		scores["software-engineer"] = specific[0].score // as strong as their strongest area
	}

	order = append(order, best)
	if best != "software-engineer" {
		order = append(order, "software-engineer")
	}
	for _, r := range specific {
		if r.id != best {
			order = append(order, r.id)
		}
	}
	return best, order, scores
}

// analyze fills res.Skills, res.Role, res.Roles and res.RoleScores.
func analyze(res *Result, repos []ghRepo, langBytes map[string]map[string]int, bio, readme string, now time.Time) {
	pt := readProfileText(bio, readme)
	acc, analyzed := weighSkills(repos, langBytes, pt, now)
	res.ReposAnalyzed = analyzed
	res.Skills = selectSkills(acc)
	res.Role, res.Roles, res.RoleScores = rankRoles(acc, pt)
}

var skillsHeader = regexp.MustCompile(`(?i)^\s*(technical\s+skills|core\s+skills|key\s+skills|skills|technologies|tech\s+stack|expertise|competencies|tools)\s*:?\s*$`)

// skillsSection returns the lines that follow "Skills"-style headings, where people list their tools.
func skillsSection(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	for i, l := range lines {
		if !skillsHeader.MatchString(l) {
			continue
		}
		taken := 0
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			out = append(out, next)
			if taken++; taken >= 12 {
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

// AnalyzeText infers skills and role from free text such as a CV. Skills count more the more often
// they are mentioned and when they are listed under a "Skills" heading; things the person says they
// are still learning are ignored. There is no recency signal in plain text, so none is used.
func AnalyzeText(text string) *Result {
	pt := readProfileText("", text)
	inSkills := signals.CountSkills(skillsSection(text))

	acc := map[string]*skillAcc{}
	for skill, n := range pt.counts {
		w := 2 + 1.5*math.Log1p(float64(n))
		if inSkills[skill] > 0 {
			w += 1.5
		}
		acc[skill] = &skillAcc{weight: w, repos: n, profile: true}
	}
	res := &Result{}
	res.Skills = selectSkills(acc)
	res.Role, res.Roles, res.RoleScores = rankRoles(acc, pt)
	return res
}
