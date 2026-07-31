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
// What this gate CANNOT do, stated here rather than in a report that ages out:
// GitHub enforces `validations.required` in the new-issue UI, and that path is
// not reachable by API. A live blank submission per form remains the only proof
// that a reporter is actually stopped, and it is not automatable. This gate
// proves the file SAYS what it must; it does not prove GitHub ENFORCED it.
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
		securityRoute := regexp.MustCompile(`(?i)vulnerab|security`)
		var found bool
		for _, l := range c.ContactLinks {
			if strings.TrimSpace(l.Name) == "" || strings.TrimSpace(l.About) == "" {
				t.Errorf("%s: %s: a contact link needs both `name` and `about`; GitHub hides one that is missing either", r.slug, r.chooserPath)
			}
			if !strings.HasPrefix(l.URL, "https://") {
				t.Errorf("%s: %s: contact link %q is not https: %q", r.slug, r.chooserPath, l.Name, l.URL)
			}
			if securityRoute.MatchString(l.Name) || securityRoute.MatchString(l.About) {
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
