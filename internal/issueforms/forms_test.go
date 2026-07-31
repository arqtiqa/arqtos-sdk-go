// Package issueforms is test-only. It holds the static gate over this
// repository's GitHub issue FORMS (`.github/ISSUE_TEMPLATE/*.yml`).
//
// Why a gate at all. A form's promise is that its fields are structured and
// individually required; a free-text template is a suggestion a reporter can
// ignore. Nothing in GitHub validates a form file before a reporter meets it —
// a malformed form is silently ignored and the repository quietly falls back
// to a blank issue box, which is exactly the state this repository was in
// before arqtiqa/arqtos-sdk-go#12. So the file that claims a field is required
// has to be checked by something.
//
// What this gate CANNOT do, stated here rather than in a report that ages out.
// Two of #12's acceptance criteria are OUTSTANDING, and neither is checkable
// from inside the estate that owns these repositories:
//
//   - AC-2 (a blank submission per form is rejected) is OUTSTANDING. GitHub
//     enforces `validations.required` in the new-issue UI, and that path is not
//     reachable by API. A live blank submission per form remains the only proof
//     that a reporter is actually stopped, and it is not automatable. This gate
//     proves the file SAYS what it must; it does not prove GitHub ENFORCED it.
//
//   - AC-6 (filing is possible without estate access) is OUTSTANDING. It
//     requires a reporter who is NOT a member of the organisation owning these
//     repositories, and no such identity exists here: the only second account
//     available at the time of writing is an organisation MEMBER, so a
//     successful filing from it would prove nothing about a stranger. Absence
//     of a statement reads as "met"; this is the statement.
//     TestIntake_When_FilingFromOutsideTheOrg_TheAnswerIsUNKNOWN keeps it
//     visible in `go test -v` output and takes the outside account by
//     environment variable, so no real handle is baked into a public file.
//
// Roots. By default the gate reads this repository. `ARQTOS_ISSUE_FORM_ROOTS`
// (colon-separated absolute paths) adds sibling checkouts, so the same rule set
// can be run over every repository carrying the forms from one place instead of
// being re-implemented per repository in whatever language that repository
// happens to use. Each root's owner/repo slug is DERIVED from its git origin —
// never passed in — so a root cannot be checked against the wrong repository's
// labels.
package issueforms

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	templateDir   = ".github/ISSUE_TEMPLATE"
	chooserConfig = "config.yml"
	workflowDir   = ".github/workflows"

	triageLabel  = "triage:needed"
	originPrefix = "intake:"
)

// ---------------------------------------------------------------------------
// the form schema, as GitHub defines it
// ---------------------------------------------------------------------------

// form is decoded with KnownFields(true), which makes the absent fields load
// bearing: any top-level key GitHub does not define is a decode error. That is
// deliberately how `projects:` is caught. `projects:` on an issue form adds
// every filed issue to a project board on arrival, which is precisely the
// auto-promotion #12 forbids — an intake issue arrives untyped, unestimated
// and unparented, and landing it on the board bypasses every field rule a
// tracked item is held to.
type form struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description"`
	Title       string    `yaml:"title"`
	Labels      []string  `yaml:"labels"`
	Assignees   []string  `yaml:"assignees"`
	Body        []element `yaml:"body"`
}

type element struct {
	Type        string     `yaml:"type"`
	ID          string     `yaml:"id"`
	Attributes  attributes `yaml:"attributes"`
	Validations struct {
		Required bool `yaml:"required"`
	} `yaml:"validations"`
}

type attributes struct {
	Label       string   `yaml:"label"`
	Description string   `yaml:"description"`
	Placeholder string   `yaml:"placeholder"`
	Value       string   `yaml:"value"`
	Render      string   `yaml:"render"`
	Multiple    bool     `yaml:"multiple"`
	Options     []option `yaml:"options"`
}

// option covers both shapes GitHub allows: a bare string (dropdown) and a
// mapping with its own `required` (checkboxes — where required is per option,
// not under `validations`).
type option struct {
	Label    string `yaml:"label"`
	Required bool   `yaml:"required"`
}

func (o *option) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return n.Decode(&o.Label)
	}
	var m struct {
		Label    string `yaml:"label"`
		Required bool   `yaml:"required"`
	}
	if err := n.Decode(&m); err != nil {
		return err
	}
	o.Label, o.Required = m.Label, m.Required
	return nil
}

// chooser is `config.yml` — not a form. It decides whether the forms are the
// only path in (blank_issues_enabled) and what else a reporter may be sent to.
type chooser struct {
	BlankIssuesEnabled *bool `yaml:"blank_issues_enabled"`
	ContactLinks       []struct {
		Name  string `yaml:"name"`
		URL   string `yaml:"url"`
		About string `yaml:"about"`
	} `yaml:"contact_links"`
}

// ---------------------------------------------------------------------------
// per-kind rules — the Story's REASONS, not just its list of fields
// ---------------------------------------------------------------------------

// slot is one semantic thing a form must ask for. It is matched against the
// field's `id`, so the check survives every rewording of a label or a
// description: the prose is free to improve, the ask cannot silently vanish.
type slot struct {
	name string
	id   *regexp.Regexp
}

type kindRule struct {
	// required slots that must each be filled by a field carrying
	// validations.required: true.
	required []slot
	// slots that must NOT be required. These are the anti-friction half, and
	// they are the reason problem.yml exists as a separate form: a confused
	// reporter files NOTHING when the only form in front of them demands a
	// reproduction. A future edit that "harmonises" the forms by adding a
	// required reproduction to problem.yml destroys the form's purpose while
	// leaving it looking healthier, so it fails here.
	forbidden []slot
}

var kindRules = map[string]kindRule{
	"bug": {
		required: []slot{
			{"what happened", regexp.MustCompile(`happen|observ`)},
			{"what was expected", regexp.MustCompile(`expect`)},
			{"version", regexp.MustCompile(`version`)},
			{"reproduction", regexp.MustCompile(`repro`)},
		},
	},
	"problem": {
		required: []slot{
			{"what you were doing", regexp.MustCompile(`doing|trying|goal`)},
			{"where you got stuck", regexp.MustCompile(`stuck|blocked`)},
		},
		forbidden: []slot{
			{"reproduction", regexp.MustCompile(`repro`)},
			{"version", regexp.MustCompile(`version`)},
		},
	},
	"enhancement": {
		required: []slot{
			// The ask is the PROBLEM. A request phrased only as a solution
			// hides the need behind one person's guess at the fix.
			{"the problem it solves", regexp.MustCompile(`problem|need`)},
			{"who benefits", regexp.MustCompile(`benefit|who|affect`)},
		},
		forbidden: []slot{
			{"a proposed solution", regexp.MustCompile(`solution|proposal|implement|design`)},
		},
	},
}

// ---------------------------------------------------------------------------
// hygiene — these files are public by definition
// ---------------------------------------------------------------------------

// leakPatterns are shapes that must never appear in a file a stranger reads.
// The list is deliberately about SHAPES, not about any estate's terms: a
// denylist of real names is a list of the things it protects, so it does not
// belong in a public repository. (arqtos-skills carries the term-list gate,
// fed from a secret at CI time; this is the shape half, which is safe to
// commit anywhere.)
var leakPatterns = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a GitHub token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,})`)},
	{"a model-provider API key", regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9]{32,})`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"an AWS access key id", regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"a private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"an email address", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)},
	// A secret reference may only ever appear as a metavariable. `op://<vault>/…`
	// teaches the shape; `op://` followed by anything else names a real vault,
	// item or field, which is estate detail even without the value.
	{"a non-metavariable secret reference", regexp.MustCompile(`op://[^<\s]`)},
	// An absolute home path names a machine's user account.
	{"an absolute home path", regexp.MustCompile(`(?:/Users/|/home/)[A-Za-z0-9._-]+/`)},
}

// ---------------------------------------------------------------------------
// what a form may say about a tool it does not ship
// ---------------------------------------------------------------------------

// safetyClaimPatterns are assertions a form is not entitled to make. A form can
// ask a reporter to redact; it cannot promise them that what they paste is
// clean. The promise is the dangerous half: a reporter who believes the output
// is safe stops reading it, which is the exact moment a credential ships. The
// instruction must therefore survive the tool being absent, buggy, or thorough
// only about the hazards someone thought of — so no wording here may rest on it.
var safetyClaimPatterns = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a claim that filling or pasting something is safe", regexp.MustCompile(`(?i)safe\s+to\s+(fill|paste|post|share|include|attach|send)`)},
	{"a bare claim that something IS safe", regexp.MustCompile(`(?i)\b(is|are|was|were|will\s+be|makes\s+it|keeps\s+it)\s+safe\b`)},
	// Deliberately NOT a bare /guarantee/: an enhancement form legitimately
	// invites "a guarantee you cannot give your own callers", and a gate that
	// fails honest prose gets switched off. The hazard is a guarantee about
	// REDACTION, so the pattern is bound to that subject.
	{"a guarantee about what redaction removes", regexp.MustCompile(`(?is)(guarantee[a-z]*\s+(that\s+)?(nothing|no\s+secret|the\s+output|redaction|it\s+omits)|proven\s+to\s+omit|cannot\s+leak|never\s+leaks|no\s+secret\s+can|strips\s+every|omits\s+every|removes\s+every)`)},
}

var (
	// redactFlagNamed: a form naming the flag by name is making a claim about a
	// binary this repository does not build and cannot pin the version of.
	redactFlagNamed = regexp.MustCompile(`--redact`)

	// reviewBeforePost: the instruction that has to be there whatever the tool
	// does. The reporter reads the output before it leaves their machine.
	reviewBeforePost = regexp.MustCompile(`(?is)\b(read|review|check)\b[^.]{0,90}\bbefore\b[^.]{0,90}\b(post|posting|paste|pasting|submit|submitting|filing|file|send|sending)\b`)

	// availabilityCond: what makes the instruction true whether or not the flag
	// has shipped. Without it, a reporter on a version that does not carry the
	// flag meets an instruction that fails, and their next move is the raw dump.
	availabilityCond = regexp.MustCompile(`(?is)(if\s+your\s+version|if\s+the\s+flag|if\s+that\s+flag|if\s+your\s+arqtos|when\s+(it\s+is\s+|it's\s+)?available|where\s+available|if\s+it\s+is\s+not|unknown\s+flag|not\s+every\s+version|older\s+version|does\s+not\s+(have|carry|recognise|recognize))`)

	// claimsFlagExists: a present-tense existence claim. Not wrong in itself —
	// wrong when the binary disagrees, which is what this gate measures.
	claimsFlagExists = regexp.MustCompile(`(?is)((that|the|this)\s+flag\s+exists|--redact\s+exists|the\s+flag\s+is\s+available|flag\s+exists\s+precisely)`)
)

// promotionPatterns are the mechanisms by which a workflow could put an
// arriving intake issue onto a project board. Note what is NOT here: an
// `on: issues` trigger. Acknowledging receipt automatically is wanted — a
// reporter with no estate access cannot tell "not yet triaged" from
// "ignored" — so banning the trigger would ban the good half with the bad.
// Only promotion itself is banned.
var promotionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`add-to-project`),
	regexp.MustCompile(`addProjectV2ItemById`),
	regexp.MustCompile(`gh\s+project\s+item-add`),
}

// ---------------------------------------------------------------------------
// loading
// ---------------------------------------------------------------------------

type loadedForm struct {
	kind string // filename stem: bug / problem / enhancement
	path string // path as reported in failures
	raw  []byte
	form form
}

type root struct {
	dir   string
	slug  string // owner/repo, derived from git origin
	forms []loadedForm
	// chooser
	chooserPath string
	chooserRaw  []byte
	chooser     chooser
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// The test runs with cwd = this package's directory: internal/issueforms.
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return abs
}

func roots(t *testing.T) []*root {
	t.Helper()
	dirs := []string{repoRoot(t)}
	if extra := os.Getenv("ARQTOS_ISSUE_FORM_ROOTS"); extra != "" {
		for _, d := range strings.Split(extra, string(os.PathListSeparator)) {
			if d = strings.TrimSpace(d); d != "" {
				dirs = append(dirs, d)
			}
		}
	}
	out := make([]*root, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, load(t, d))
	}
	return out
}

func load(t *testing.T, dir string) *root {
	t.Helper()
	r := &root{dir: dir, slug: originSlug(t, dir)}

	entries, err := os.ReadDir(filepath.Join(dir, templateDir))
	if err != nil {
		t.Fatalf("%s: no issue-form directory (%s): %v — with no forms the repository falls back to a blank issue box", r.slug, templateDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		path := filepath.Join(dir, templateDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: reading %s: %v", r.slug, name, err)
		}
		rel := filepath.Join(templateDir, name)
		if name == chooserConfig {
			r.chooserPath, r.chooserRaw = rel, raw
			if err := strictDecode(raw, &r.chooser); err != nil {
				t.Fatalf("%s: %s does not decode as an issue-chooser config: %v", r.slug, rel, err)
			}
			continue
		}
		lf := loadedForm{
			kind: strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml"),
			path: rel,
			raw:  raw,
		}
		if err := strictDecode(raw, &lf.form); err != nil {
			t.Fatalf("%s: %s does not decode as a GitHub issue form: %v\n"+
				"a form GitHub cannot parse is silently ignored — the repository shows a blank issue box instead", r.slug, rel, err)
		}
		r.forms = append(r.forms, lf)
	}
	sort.Slice(r.forms, func(i, j int) bool { return r.forms[i].kind < r.forms[j].kind })
	return r
}

func strictDecode(raw []byte, into any) error {
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		return err
	}
	return nil
}

// originSlug derives owner/repo from the checkout's git origin. Derived, never
// supplied: a hand-passed slug is how a root gets checked against another
// repository's labels and reports a pass it did not earn.
func originSlug(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("%s: cannot read the git origin, so the repository this root belongs to is unknown: %v", dir, err)
	}
	url := strings.TrimSpace(string(out))
	url = strings.TrimSuffix(url, ".git")
	if i := strings.Index(url, "github.com"); i >= 0 {
		url = url[i+len("github.com"):]
	}
	slug := strings.Trim(strings.TrimPrefix(url, ":"), "/")
	if strings.Count(slug, "/") != 1 {
		t.Fatalf("%s: git origin %q does not yield an owner/repo slug", dir, string(out))
	}
	return slug
}

// ---------------------------------------------------------------------------
// gh — the network half. Absent credentials produce UNKNOWN, never a pass.
// ---------------------------------------------------------------------------

var errNoGH = errors.New("gh is unavailable or unauthenticated")

func ghReady() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("%w: %v", errNoGH, err)
	}
	if out, err := exec.Command("gh", "auth", "status").CombinedOutput(); err != nil {
		return fmt.Errorf("%w: gh auth status: %v: %s", errNoGH, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gh(args ...string) (string, error) {
	out, err := exec.Command("gh", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// skipUnknown records an UNKNOWN result. Unknown is not False and it is not
// True: the check could not determine its answer, and saying so is the whole
// point. t.Skip keeps it visible in `go test -v` output rather than letting it
// read as a pass.
func skipUnknown(t *testing.T, reason error) {
	t.Helper()
	t.Skipf("UNKNOWN — this check could not be determined here, it did NOT pass: %v", reason)
}

// ---------------------------------------------------------------------------
// helpers over a loaded form
// ---------------------------------------------------------------------------

// fields returns the interactive elements — everything but the markdown blocks,
// which are prose shown to the reporter and collect nothing.
func fields(f form) []element {
	var out []element
	for _, e := range f.Body {
		if e.Type != "markdown" {
			out = append(out, e)
		}
	}
	return out
}

func isRequired(e element) bool {
	if e.Validations.Required {
		return true
	}
	// checkboxes carry required per option, not under validations.
	for _, o := range e.Attributes.Options {
		if o.Required {
			return true
		}
	}
	return false
}

func originLabels(f form) []string {
	var out []string
	for _, l := range f.Labels {
		if strings.HasPrefix(l, originPrefix) {
			out = append(out, l)
		}
	}
	return out
}

func eachRoot(t *testing.T, fn func(t *testing.T, r *root)) {
	t.Helper()
	for _, r := range roots(t) {
		t.Run(r.slug, func(t *testing.T) { fn(t, r) })
	}
}

// ---------------------------------------------------------------------------
// the gate
// ---------------------------------------------------------------------------

func TestIssueForms_When_ARepositoryTakesIntake_TheWholeFormSetIsPresent(t *testing.T) {
	eachRoot(t, func(t *testing.T, r *root) {
		want := make([]string, 0, len(kindRules))
		for k := range kindRules {
			want = append(want, k)
		}
		sort.Strings(want)

		got := map[string]bool{}
		for _, f := range r.forms {
			got[f.kind] = true
		}
		for _, k := range want {
			if !got[k] {
				t.Errorf("%s: %s/%s.yml is missing — the three forms are not interchangeable, each exists because the others turn a reporter away",
					r.slug, templateDir, k)
			}
		}
		if r.chooserRaw == nil {
			t.Errorf("%s: %s/%s is missing — without it a blank issue box remains open and the forms are optional",
				r.slug, templateDir, chooserConfig)
		}
	})
}

func TestIssueForms_When_AFormIsParsed_ItsSchemaIsValid(t *testing.T) {
	eachRoot(t, func(t *testing.T, r *root) {
		for _, lf := range r.forms {
			f := lf.form
			if strings.TrimSpace(f.Name) == "" {
				t.Errorf("%s: %s: `name` is empty — it is the entry the reporter picks from the chooser", r.slug, lf.path)
			}
			if strings.TrimSpace(f.Description) == "" {
				t.Errorf("%s: %s: `description` is empty — it is the one line that tells a reporter this is their form", r.slug, lf.path)
			}
			if len(f.Body) == 0 {
				t.Errorf("%s: %s: `body` is empty", r.slug, lf.path)
			}

			seen := map[string]bool{}
			for i, e := range f.Body {
				where := fmt.Sprintf("%s: %s: body[%d]", r.slug, lf.path, i)
				switch e.Type {
				case "markdown":
					if strings.TrimSpace(e.Attributes.Value) == "" {
						t.Errorf("%s: a markdown block with no `value`", where)
					}
					if e.ID != "" {
						t.Errorf("%s: a markdown block carries an `id` — it collects nothing", where)
					}
					if e.Validations.Required {
						t.Errorf("%s: a markdown block is marked required — GitHub rejects the form and the repository falls back to a blank issue box", where)
					}
				case "input", "textarea", "dropdown", "checkboxes":
					if strings.TrimSpace(e.Attributes.Label) == "" {
						t.Errorf("%s: a %s with no `label`", where, e.Type)
					}
					if e.ID == "" {
						// Not GitHub's rule; this gate's. Every semantic check
						// below matches on `id`, and a field without one is
						// invisible to them — it could stop being required and
						// nothing would notice.
						t.Errorf("%s: a %s with no `id` — the required-field checks address fields by id", where, e.Type)
					}
					if (e.Type == "dropdown" || e.Type == "checkboxes") && len(e.Attributes.Options) == 0 {
						t.Errorf("%s: a %s with no `options`", where, e.Type)
					}
					if e.Attributes.Render != "" && e.Type != "textarea" {
						t.Errorf("%s: `render` is only valid on a textarea", where)
					}
				default:
					t.Errorf("%s: unknown element type %q", where, e.Type)
				}
				if e.ID != "" {
					if seen[e.ID] {
						t.Errorf("%s: duplicate id %q — GitHub rejects the form", where, e.ID)
					}
					seen[e.ID] = true
				}
			}

			// Belt and braces over KnownFields: name the offender rather than
			// leaving a reader to interpret a decoder message.
			if regexp.MustCompile(`(?m)^projects:`).Match(lf.raw) {
				t.Errorf("%s: %s declares `projects:` — that promotes every arriving report onto a board, "+
					"which is exactly what an untyped, unestimated, unparented intake issue must not do", r.slug, lf.path)
			}
		}
	})
}

func TestIssueForms_When_AFormDeclaresItsKind_TheRightFieldsAreRequired(t *testing.T) {
	eachRoot(t, func(t *testing.T, r *root) {
		for _, lf := range r.forms {
			rule, ok := kindRules[lf.kind]
			if !ok {
				t.Errorf("%s: %s is not one of the three intake kinds — a fourth form dilutes the routing this gate rests on", r.slug, lf.path)
				continue
			}
			fs := fields(lf.form)

			for _, s := range rule.required {
				var matched, required bool
				for _, e := range fs {
					if s.id.MatchString(strings.ToLower(e.ID)) {
						matched = true
						if isRequired(e) {
							required = true
						}
					}
				}
				switch {
				case !matched:
					t.Errorf("%s: %s asks nothing about %s (no field id matches %v) — the report arrives needing a round trip to become actionable",
						r.slug, lf.path, s.name, s.id)
				case !required:
					t.Errorf("%s: %s asks about %s but does not require it — an unrequired field is a suggestion, and a suggestion is what a free-text template already was",
						r.slug, lf.path, s.name)
				}
			}

			for _, s := range rule.forbidden {
				for _, e := range fs {
					if s.id.MatchString(strings.ToLower(e.ID)) && isRequired(e) {
						t.Errorf("%s: %s requires %s (field %q). That is the friction this form exists to remove — a reporter who cannot supply it files nothing at all, and the report you lose is the one you most wanted",
							r.slug, lf.path, s.name, e.ID)
					}
				}
			}
		}
	})
}

func TestIssueForms_When_AReportArrives_ItsOriginIsSetByTheFormNotTheReporter(t *testing.T) {
	eachRoot(t, func(t *testing.T, r *root) {
		origins := map[string][]string{}
		for _, lf := range r.forms {
			var hasTriage bool
			for _, l := range lf.form.Labels {
				if l == triageLabel {
					hasTriage = true
				}
			}
			if !hasTriage {
				t.Errorf("%s: %s does not apply %q — an unlabelled report is one nobody is holding", r.slug, lf.path, triageLabel)
			}

			o := originLabels(lf.form)
			if len(o) != 1 {
				t.Errorf("%s: %s applies %d %s* labels, want exactly 1 — origin decides how much context a triager may assume, so it cannot be absent or ambiguous",
					r.slug, lf.path, len(o), originPrefix)
				continue
			}
			origins[o[0]] = append(origins[o[0]], lf.kind)

			// Origin must not be a question. A reporter cannot reliably
			// classify themselves, and a dropdown asking them to is how a
			// mislabelled report gets triaged against the wrong assumptions.
			for _, e := range fields(lf.form) {
				lower := strings.ToLower(e.ID + " " + e.Attributes.Label)
				if strings.Contains(lower, "origin") || strings.Contains(lower, "tenant or external") {
					t.Errorf("%s: %s asks the reporter for their origin (field %q) — origin is set by WHICH FORM they used, never asked",
						r.slug, lf.path, e.ID)
				}
			}
		}
		if len(origins) > 1 {
			t.Errorf("%s: the forms disagree about their reporter population: %v. One repository serves one population — whoever can reach its issue tab — so a per-form split means one of the forms is mislabelling every report it takes",
				r.slug, origins)
		}
	})
}

func TestIssueForms_When_TheChooserIsShown_BlankIssuesAreClosedAndSecurityIsRoutedPrivately(t *testing.T) {
	eachRoot(t, func(t *testing.T, r *root) {
		if r.chooserRaw == nil {
			t.Fatalf("%s: no %s", r.slug, chooserConfig)
		}
		c := r.chooser
		switch {
		case c.BlankIssuesEnabled == nil:
			t.Errorf("%s: %s omits `blank_issues_enabled` — it defaults to TRUE, so the forms become one option beside an empty box and every required field is optional again",
				r.slug, r.chooserPath)
		case *c.BlankIssuesEnabled:
			t.Errorf("%s: %s sets blank_issues_enabled: true — the forms are then bypassable", r.slug, r.chooserPath)
		}

		if len(c.ContactLinks) == 0 {
			t.Fatalf("%s: %s declares no contact_links — a vulnerability then has no route except a public issue, which is a public advisory with no fix available yet",
				r.slug, r.chooserPath)
		}
		var found bool
		for _, l := range c.ContactLinks {
			if strings.TrimSpace(l.Name) == "" || strings.TrimSpace(l.About) == "" {
				t.Errorf("%s: %s: a contact link needs both `name` and `about`; GitHub hides one that is missing either", r.slug, r.chooserPath)
			}
			if !strings.HasPrefix(l.URL, "https://") {
				t.Errorf("%s: %s: contact link %q is not https: %q", r.slug, r.chooserPath, l.Name, l.URL)
			}
			if securityRouteRE.MatchString(l.Name) || securityRouteRE.MatchString(l.About) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: %s has no contact link that routes a security report away from the public issue tracker", r.slug, r.chooserPath)
		}
	})
}

// The 404 trap. Only one of the repositories carrying these forms is
// world-readable. A contact link on a public repository that points into a
// private one renders, is clicked, and returns 404 — which reads as "there is
// no way to report this", the exact outcome intake exists to prevent. So every
// off-repo target has to be readable by whoever can read the repository the
// link sits on: either the same repository, or a public one.
func TestIssueForms_When_AContactLinkLeavesTheRepo_ItsTargetIsReadableByThatRepoReader(t *testing.T) {
	if err := ghReady(); err != nil {
		skipUnknown(t, fmt.Errorf("target visibility is a live repository property: %w", err))
	}
	slugInURL := regexp.MustCompile(`^https://github\.com/([^/]+)/([^/#?]+)`)
	visible := map[string]bool{}

	eachRoot(t, func(t *testing.T, r *root) {
		// Counted and logged: this check passes vacuously for a repository whose
		// every contact link stays at home, and a reader of the output is
		// entitled to know which of the two happened.
		offRepo := 0
		defer func() {
			t.Logf("%s: %d contact-link target(s) leave this repository and had their visibility resolved", r.slug, offRepo)
		}()
		for _, l := range r.chooser.ContactLinks {
			m := slugInURL.FindStringSubmatch(l.URL)
			if m == nil {
				continue // not a github.com repository target; nothing to resolve
			}
			target := m[1] + "/" + strings.TrimSuffix(m[2], ".git")
			if target == r.slug {
				continue // same repository: readable by definition
			}
			offRepo++
			pub, cached := visible[target]
			if !cached {
				out, err := gh("api", "repos/"+target, "--jq", ".private")
				if err != nil {
					t.Errorf("%s: %s: cannot resolve the visibility of contact-link target %s: %v", r.slug, r.chooserPath, target, err)
					continue
				}
				pub = out == "false"
				visible[target] = pub
			}
			if !pub {
				t.Errorf("%s: %s: contact link %q points at %s, which is PRIVATE. A reporter who can reach this repository's issue tab may not be a collaborator there, and a 404 reads as \"no way to report\"",
					r.slug, r.chooserPath, l.Name, target)
			}
		}
	})
}

// A label a form applies but the repository does not define is dropped on
// arrival, in silence: the issue is created, the form looks like it worked, and
// the report is not in anybody's triage queue.
func TestIssueForms_When_AFormAppliesALabel_ThatLabelExistsInItsRepo(t *testing.T) {
	if err := ghReady(); err != nil {
		skipUnknown(t, fmt.Errorf("label existence is a live repository property: %w", err))
	}
	eachRoot(t, func(t *testing.T, r *root) {
		out, err := gh("label", "list", "--repo", r.slug, "--limit", "200", "--json", "name", "--jq", ".[].name")
		if err != nil {
			t.Fatalf("%s: cannot list labels: %v", r.slug, err)
		}
		have := map[string]bool{}
		for _, n := range strings.Split(out, "\n") {
			if n = strings.TrimSpace(n); n != "" {
				have[n] = true
			}
		}
		if len(have) == 0 {
			t.Fatalf("%s: label listing came back empty — no claim can be made over an empty set", r.slug)
		}
		for _, lf := range r.forms {
			for _, l := range lf.form.Labels {
				if !have[l] {
					t.Errorf("%s: %s applies label %q which does not exist in %s — GitHub drops it silently and the report lands unlabelled",
						r.slug, lf.path, l, r.slug)
				}
			}
		}
	})
}

func TestIssueForms_When_TheFormsArePublic_NoSecretOrAccountIdentifierLeaks(t *testing.T) {
	eachRoot(t, func(t *testing.T, r *root) {
		files := map[string][]byte{}
		for _, lf := range r.forms {
			files[lf.path] = lf.raw
		}
		if r.chooserRaw != nil {
			files[r.chooserPath] = r.chooserRaw
		}
		if len(files) == 0 {
			t.Fatalf("%s: nothing scanned — a clean result over an empty set is not a clean result", r.slug)
		}
		paths := make([]string, 0, len(files))
		for p := range files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			for _, lp := range leakPatterns {
				if loc := lp.re.FindIndex(files[p]); loc != nil {
					line := 1 + strings.Count(string(files[p][:loc[0]]), "\n")
					// The match itself is never printed: it is the thing the
					// check exists to keep out of a log.
					t.Errorf("%s: %s:%d matches %s — every identifier in a file a stranger reads must be synthetic",
						r.slug, p, line, lp.what)
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// the security route, followed to a terminal state
// ---------------------------------------------------------------------------
//
// The check the first cut of this gate did not have, and the reason the branch
// shipped a dead end: it resolved the contact link's target REPOSITORY and
// stopped there. The target was readable, so the check passed — and the document
// behind it told the reporter to press a button that GitHub was not rendering,
// because private vulnerability reporting was measurably DISABLED. One hop
// verified, terminal state unverified, and a reporter following the link ends
// nowhere. So this follows the whole chain, and the only accepted endings are
// ones whose existence was MEASURED.

var (
	advisoryNewURL = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/security/advisories/new/?$`)
	// unanchored, for finding the route named inside a policy document
	advisoryNewInProse = regexp.MustCompile(`https://github\.com/([^/\s)]+)/([^/\s)]+)/security/advisories/new`)
	blobURL            = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/blob/([^/]+)/(.+)$`)
	addressInProse     = regexp.MustCompile(`(?:mailto:)?[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	securityRouteRE    = regexp.MustCompile(`(?i)vulnerab|security`)
)

// pvrMeasured answers whether private vulnerability reporting is on, from the
// response BODY. Never from the exit code: `gh api` exits 0 for
// `{"enabled":false}`, which is the answer that matters most here.
func pvrMeasured(slug string) (bool, string) {
	out, err := gh("api", "repos/"+slug+"/private-vulnerability-reporting")
	if err != nil {
		return false, fmt.Sprintf("the setting endpoint does not answer for %s — GitHub offers private vulnerability reporting on PUBLIC repositories only, so a private repository cannot host this channel at all (%v)", slug, err)
	}
	if strings.Contains(out, `"enabled":true`) {
		return true, fmt.Sprintf("GET repos/%s/private-vulnerability-reporting → %s", slug, out)
	}
	return false, fmt.Sprintf("GET repos/%s/private-vulnerability-reporting → %s — the button that URL opens is not being rendered", slug, out)
}

func securityLinks(r *root) []string {
	var out []string
	for _, l := range r.chooser.ContactLinks {
		if securityRouteRE.MatchString(l.Name) || securityRouteRE.MatchString(l.About) {
			out = append(out, l.URL)
		}
	}
	return out
}

// readPolicyDoc prefers the LOCAL working tree when the target repository is one
// of the loaded roots. A gate has to fail on the BRANCH that proposes a dead end
// rather than wait for the dead end to become `main`'s problem; once merged the
// two agree. Which source answered is always logged.
func readPolicyDoc(all []*root, owner, repo, ref, path string) (content, source string, err error) {
	slug := owner + "/" + strings.TrimSuffix(repo, ".git")
	for _, r := range all {
		if r.slug != slug {
			continue
		}
		p := filepath.Join(r.dir, filepath.FromSlash(path))
		b, rerr := os.ReadFile(p)
		if rerr == nil {
			return string(b), fmt.Sprintf("%s/%s in the local working tree (checked ahead of %s@%s)", slug, path, slug, ref), nil
		}
	}
	out, gerr := gh("api", fmt.Sprintf("repos/%s/contents/%s?ref=%s", slug, path, ref), "-H", "Accept: application/vnd.github.raw")
	if gerr != nil {
		return "", "", fmt.Errorf("cannot read %s at %s in %s: %w", path, ref, slug, gerr)
	}
	return out, fmt.Sprintf("%s@%s:%s (fetched)", slug, ref, path), nil
}

// followSecurityRoute walks one contact-link URL to a terminal state. depth is
// capped at one indirection: a chooser entry may point at a policy document, and
// that document must name the channel. A route needing more hops than that is a
// route no reporter finishes.
func followSecurityRoute(all []*root, url string, depth int) (reached bool, why string) {
	if depth > 1 {
		return false, fmt.Sprintf("%s is more than one indirection from a channel — each extra hop is somewhere a reporter stops", url)
	}
	if m := advisoryNewURL.FindStringSubmatch(url); m != nil {
		slug := m[1] + "/" + m[2]
		on, detail := pvrMeasured(slug)
		if on {
			return true, "the private advisory form on " + slug + ": " + detail
		}
		return false, "the URL is the advisory form on " + slug + " but " + detail
	}
	if m := blobURL.FindStringSubmatch(url); m != nil {
		content, source, err := readPolicyDoc(all, m[1], m[2], m[3], m[4])
		if err != nil {
			return false, err.Error()
		}
		for _, hit := range advisoryNewInProse.FindAllString(content, -1) {
			if ok, sub := followSecurityRoute(all, hit, depth+1); ok {
				return true, source + " names " + sub
			} else if depth == 0 {
				// Record the rejected candidate: a document naming a dead URL is
				// worse than one naming none, because it looks answered.
				why = source + " names " + hit + " — " + sub
			}
		}
		if addressInProse.MatchString(content) {
			return true, source + " names an address to write to, which needs nothing enabled on GitHub to work"
		}
		if why != "" {
			return false, why
		}
		return false, source + " names NO reachable channel: no advisory-form URL and no address. Telling a reporter to \"use private vulnerability reporting\" is not a route — the feature is a per-repository setting, and a reporter cannot tell a missing button from their own confusion"
	}
	return false, fmt.Sprintf("%s is not a shape this gate can follow to a terminal state, and a route nobody followed is a route nobody verified", url)
}

func TestIssueForms_When_TheSecurityRouteIsFollowed_ItEndsAtAChannelMeasuredToExist(t *testing.T) {
	if err := ghReady(); err != nil {
		skipUnknown(t, fmt.Errorf("whether a disclosure channel exists is a live repository setting: %w", err))
	}
	all := roots(t)
	for _, r := range all {
		t.Run(r.slug, func(t *testing.T) {
			links := securityLinks(r)
			if len(links) == 0 {
				t.Fatalf("%s: %s routes no contact link at a security report", r.slug, r.chooserPath)
			}
			for _, u := range links {
				reached, why := followSecurityRoute(all, u, 0)
				if !reached {
					t.Errorf("%s: %s: the security route DEAD-ENDS.\n  from: %s\n  why:  %s\n"+
						"A reporter who cannot reach a private channel either files the vulnerability as a public issue — a public advisory with no fix available — or files nothing.",
						r.slug, r.chooserPath, u, why)
					continue
				}
				t.Logf("%s: %s → terminal state: %s", r.slug, u, why)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// what a form may promise about redaction
// ---------------------------------------------------------------------------

func TestIssueForms_When_AFormAsksForDiagnostics_ItPromisesNoSafetyAndSurvivesTheToolBeingAbsent(t *testing.T) {
	eachRoot(t, func(t *testing.T, r *root) {
		scanned := 0
		for _, lf := range r.forms {
			scanned++
			for _, sc := range safetyClaimPatterns {
				if loc := sc.re.FindIndex(lf.raw); loc != nil {
					line := 1 + strings.Count(string(lf.raw[:loc[0]]), "\n")
					t.Errorf("%s: %s:%d asserts %s. A form may ask a reporter to redact; it may not tell them the result is clean — a reporter who believes it stops reading the output, and that is the moment a credential ships. Say what to run and tell them to review it.",
						r.slug, lf.path, line, sc.what)
				}
			}
			if !redactFlagNamed.Match(lf.raw) {
				continue
			}
			if !reviewBeforePost.Match(lf.raw) {
				t.Errorf("%s: %s names `--redact` but never tells the reporter to review the output before it leaves their machine. That instruction is the only part of this that does not depend on a tool behaving.",
					r.slug, lf.path)
			}
			if !availabilityCond.Match(lf.raw) {
				t.Errorf("%s: %s names `--redact` unconditionally. This repository does not build that binary and cannot pin which version a reporter runs, so the instruction has to hold when the flag is absent too — a reporter who hits `unknown flag: --redact` and has no fallback pastes the raw output.",
					r.slug, lf.path)
			}
		}
		// The chooser's RENDERED text too — a contact link's name and about are
		// shown to the reporter, so a safety promise can hide there. Its comments
		// deliberately are not scanned: they reach no reporter, and they are where
		// the measurements behind these files are recorded.
		for _, l := range r.chooser.ContactLinks {
			scanned++
			shown := l.Name + " " + l.About
			for _, sc := range safetyClaimPatterns {
				if sc.re.MatchString(shown) {
					t.Errorf("%s: %s: contact link %q asserts %s in text the reporter is shown",
						r.slug, r.chooserPath, l.Name, sc.what)
				}
			}
		}
		if scanned == 0 {
			t.Fatalf("%s: nothing scanned — a clean result over an empty set is not a clean result", r.slug)
		}
	})
}

// The live half: a present-tense claim about a flag, checked against the binary.
// A form saying "that flag exists" while `arqtos doctor --help` does not mention
// it is a false instruction shipped to a stranger.
func TestIssueForms_When_AFormNamesADoctorFlag_ItsPresenceIsMeasuredNotAssumed(t *testing.T) {
	all := roots(t)
	type named struct {
		slug, path string
		raw        []byte
	}
	var naming []named
	for _, r := range all {
		for _, lf := range r.forms {
			if redactFlagNamed.Match(lf.raw) {
				naming = append(naming, named{r.slug, lf.path, lf.raw})
			}
		}
	}
	if len(naming) == 0 {
		t.Skip("no form names `arqtos doctor --redact`; nothing to measure")
	}
	if _, err := exec.LookPath("arqtos"); err != nil {
		skipUnknown(t, fmt.Errorf("`arqtos` is not on PATH here, so whether `--redact` exists cannot be determined: %w", err))
	}
	// The VALUE decides. `arqtos doctor --help` exits 0 either way.
	out, _ := exec.Command("arqtos", "doctor", "--help").CombinedOutput()
	present := strings.Contains(string(out), "--redact")
	t.Logf("measured: `arqtos doctor --help` %s `--redact`", map[bool]string{true: "lists", false: "does NOT list"}[present])
	if present {
		return
	}
	for _, n := range naming {
		if loc := claimsFlagExists.FindIndex(n.raw); loc != nil {
			line := 1 + strings.Count(string(n.raw[:loc[0]]), "\n")
			t.Errorf("%s: %s:%d states that the flag EXISTS, and the installed binary disagrees — `arqtos doctor --help` does not list `--redact`. A present-tense claim about a sibling branch's flag is false for every reporter running a release.",
				n.slug, n.path, line)
		}
		if !availabilityCond.Match(n.raw) {
			t.Errorf("%s: %s names `--redact` with no fallback, and the installed binary does not have it. The reporter's next move after `unknown flag` is the raw dump this field exists to prevent.",
				n.slug, n.path)
		}
	}
}

// AC-6 — filing without estate access — is OUTSTANDING and cannot be closed
// from here. This test exists so that fact is reported rather than inferred from
// its absence, and so the day a genuinely outside identity exists the check has
// somewhere to live. The account is supplied by environment variable: a real
// handle does not belong in a file a stranger reads.
func TestIntake_When_FilingFromOutsideTheOrg_TheAnswerIsUNKNOWN(t *testing.T) {
	all := roots(t)
	owner := strings.SplitN(all[0].slug, "/", 2)[0]

	acct := strings.TrimSpace(os.Getenv("ARQTOS_INTAKE_OUTSIDE_ACCOUNT"))
	if acct == "" {
		skipUnknown(t, fmt.Errorf("AC-6 OUTSTANDING: no outside identity was supplied (ARQTOS_INTAKE_OUTSIDE_ACCOUNT is unset), so whether a reporter with no access to the %s estate can file was NOT tested", owner))
	}
	if err := ghReady(); err != nil {
		skipUnknown(t, fmt.Errorf("AC-6 OUTSTANDING: membership of the supplied account cannot be resolved: %w", err))
	}
	if _, err := gh("api", fmt.Sprintf("orgs/%s/members/%s", owner, acct)); err == nil {
		skipUnknown(t, fmt.Errorf("AC-6 OUTSTANDING: the supplied account is a MEMBER of %s, so a successful filing from it proves nothing about a stranger — GET orgs/%s/members/<acct> answered, which is the 204 that means 'is a member'", owner, owner))
	}
	skipUnknown(t, fmt.Errorf("AC-6 OUTSTANDING: the supplied account is outside %s, but filing is a new-issue UI action and cannot be driven by API — the live filing remains to be done by hand", owner))
}

func TestIssueForms_When_IntakeArrives_NothingPromotesItOntoTheBoard(t *testing.T) {
	eachRoot(t, func(t *testing.T, r *root) {
		dir := filepath.Join(r.dir, workflowDir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return // no workflows at all: nothing can promote anything
			}
			t.Fatalf("%s: reading %s: %v", r.slug, workflowDir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("%s: reading %s/%s: %v", r.slug, workflowDir, name, err)
			}
			for _, p := range promotionPatterns {
				if p.Match(raw) {
					t.Errorf("%s: %s/%s matches %v — an intake issue must sit at %q until an operator either converts it into a fully fielded work item or closes it with a reason",
						r.slug, workflowDir, name, p, triageLabel)
				}
			}
		}
	})
}
