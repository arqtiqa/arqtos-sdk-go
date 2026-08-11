// Package manifest is the connector manifest schema (a connector.yml a connector
// author ships alongside their code) declaring the connector's name, the
// connector-class it Implements, its runtime Kind (declarative | provider |
// native), capabilities/supports, its refs-only Auth wiring, and — for
// out-of-process providers — the minimum host version it requires. Parse is
// strict (unknown fields rejected, mirroring skillspec.Parse); Validate closes
// the Kind/Implements enums, closes the capability vocabulary against the
// class the manifest implements, and enforces the refs-only Auth invariant: an
// Auth value must be an op:// secret reference or a bare environment-variable
// NAME, never literal secret material.
package manifest

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/arqtiqa/arqtos-sdk-go/codeci"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
	"github.com/arqtiqa/arqtos-sdk-go/tracker"
)

// Kind is a connector's runtime shape.
type Kind string

const (
	KindDeclarative Kind = "declarative"
	KindProvider    Kind = "provider"
	KindNative      Kind = "native"
)

// knownImplements is the closed set of connector classes a manifest may
// declare in Implements. It is DERIVED from connector.Classes(), not a second
// list beside it, so the manifest enum cannot drift out of sync with the SDK's
// known classes.
//
// The distinction is not cosmetic. When this was a hand-written map, adding a
// class to the connector package left it undeclarable in a manifest, and the
// symptom was a valid connector rejected by the host with "unknown implements"
// — pointing at the manifest, which was correct. Deriving it means a class
// added to connector.classes shows up here with no edit at all.
var knownImplements = func() map[connector.Class]bool {
	all := connector.Classes()
	m := make(map[connector.Class]bool, len(all))
	for _, c := range all {
		m[c] = true
	}
	return m
}()

// classCapabilities maps a connector class to the CLOSED capability
// vocabulary of that class, so [Doc.Validate] can reject a capability no host
// will ever act on.
//
// Each entry composes the class package's own published vocabulary rather
// than restating it, so a capability added to a class shows up here without
// an edit, and one removed cannot linger.
//
// # Why Validate has to do this
//
// Capabilities is []connector.Capability, and connector.Capability is a
// string type: YAML unmarshals ANY string into it. The typing buys real
// things at Go call sites — no conversion, comparison against the published
// constants — but it is nominal, and it stops nothing coming out of a file.
// Before this check, a manifest declaring "batch-resolve" (hyphen), "reed",
// or anything else at all validated clean, and only a full credconform run
// against a live connector caught it.
//
// Validate is what a host runs BEFORE it loads anything. A capability it does
// not recognise is a capability it will not use, and at that point a
// misspelling is indistinguishable from a capability that has yet to ship —
// so the connector silently loses the behaviour its author believed they had
// declared.
//
// # A class with no registered vocabulary
//
// A class in knownImplements but absent here has its capabilities accepted
// unchecked, rather than rejected wholesale. A class whose vocabulary has not
// been published yet must not have EVERY capability refused — that would make
// the manifest schema the thing that blocks a new connector class from
// shipping. The class's own conformance harness is where its vocabulary is
// enforced until it registers here. Every class the SDK ships today is
// registered, so this is a forward-compatibility allowance, not a live hole.
var classCapabilities = map[connector.Class]connector.Capabilities{
	connector.ClassCredentialLoader: credential.KnownCapabilities(),
	connector.ClassRoster:           roster.KnownCapabilities(),
	connector.ClassCodeCI:           codeci.KnownCapabilities(),
	connector.ClassTracker:          tracker.KnownCapabilities(),

	// Authenticator's vocabulary is EMPTY at v1, deliberately: the flow is
	// operator-interactive only, and a device-code path, a machine-principal
	// path, a refresh path and a step-up path were each identified and NOT
	// added, because a capability nothing gates on is the speculative feature
	// flag this SDK's capability documentation argues against.
	//
	// An empty registration is not the same as no registration. Registered-and-
	// empty CLOSES the vocabulary — every capability is refused — whereas an
	// absent entry passes them through unchecked. Empty is the correct value
	// here, not a placeholder.
	//
	// ⚠️ It is spelled inline rather than as authenticator.KnownCapabilities()
	// only because the contract package does not exist yet. When it lands, this
	// entry becomes that call like its four siblings, and the literal goes.
	connector.ClassAuthenticator: connector.Capabilities{},
}

// envNameRE matches a bare environment-variable NAME (e.g. INFISICAL_TOKEN):
// upper-case letters, digits, and underscores, not starting with a digit.
// It is deliberately strict so literal secret material (mixed case,
// punctuation, base64/hex-looking tokens) never slips through as an "env
// name" — Auth values are refs-or-names only, never the material itself.
var envNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// Doc is a connector manifest.
type Doc struct {
	Name          string          `yaml:"name"`
	Implements    connector.Class `yaml:"implements"`
	Kind          Kind            `yaml:"kind"`
	SchemaVersion string          `yaml:"schema_version,omitempty"`
	// Capabilities is what this connector DECLARES it can do, drawn from the
	// capability vocabulary of the class it implements (for
	// CredentialLoader, credential.KnownCapabilities()). It is typed rather
	// than free-form strings because a host plans its call pattern from this
	// list: batch resolution, for one, is used only where it is declared.
	//
	// [Doc.Validate] rejects a name outside that vocabulary, so a typo is
	// caught by the host before it loads the connector rather than only by a
	// conformance run against a live one.
	//
	// The declaration is also a promise the conformance harness checks
	// against the running connector. Declaring a capability that is not
	// implemented fails conformance — it is worse than declaring nothing,
	// because the host plans for a capability that is not there.
	Capabilities   []connector.Capability `yaml:"capabilities,omitempty"`
	Supports       map[string]string      `yaml:"supports,omitempty"`
	Auth           map[string]string      `yaml:"auth,omitempty"`
	MinHostVersion string                 `yaml:"min_host_version,omitempty"`
}

// Declares reports whether the manifest declares capability c.
//
// It answers only what the manifest SAYS. Whether the connector actually
// implements it is a separate question, and the one conformance asks.
func (d Doc) Declares(c connector.Capability) bool {
	return connector.Capabilities(d.Capabilities).Has(c)
}

// Parse strictly decodes a connector manifest, rejecting unknown fields.
func Parse(b []byte) (Doc, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var d Doc
	if err := dec.Decode(&d); err != nil {
		return Doc{}, fmt.Errorf("manifest: %w", err)
	}
	return d, nil
}

// Validate requires Name, closes the Kind and Implements enums, closes the
// Capabilities vocabulary against the class in Implements, enforces the
// refs-only Auth invariant on every entry, and requires MinHostVersion when
// Kind is provider (an out-of-process connector the host must dial by
// contract version; declarative/native connectors carry no such gate).
//
// It is deliberately runnable with no connector present: it is what a host
// checks before it loads anything, and everything it can decide from the
// manifest alone it decides here rather than deferring to a conformance run
// that needs a live connector and fixtures.
func (d Doc) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	switch d.Kind {
	case KindDeclarative, KindProvider, KindNative:
	default:
		return fmt.Errorf("manifest: unknown kind %q", d.Kind)
	}
	if !knownImplements[d.Implements] {
		return fmt.Errorf("manifest: unknown implements %q", d.Implements)
	}
	if err := d.validateCapabilities(); err != nil {
		return err
	}
	for name, v := range d.Auth {
		if err := validateAuthEntry(v); err != nil {
			return fmt.Errorf("manifest: auth[%s]: %w", name, err)
		}
	}
	if d.Kind == KindProvider && d.MinHostVersion == "" {
		return fmt.Errorf("manifest: min_host_version is required for kind: provider")
	}
	return nil
}

// RequireHost is the min_host_version NEGOTIATION: it reports whether a host
// at hostVersion is new enough to dial the connector this manifest describes.
//
// [Doc.Validate] proves the field is PRESENT for a provider. That is a
// different question from whether it is SATISFIED, and only the second one
// keeps an incompatible provider out of a running host. Before this existed,
// min_host_version was a string a provider author wrote and nothing ever read
// — a declaration with no consequence, which is indistinguishable from no
// declaration at all.
//
// # It fails CLOSED, and that is the opposite default from error classification
//
// An absent or unparseable version — on EITHER side — is refused. A host that
// cannot establish it is new enough is a host that must not dial: the whole
// point of the field is that the provider is asserting it needs behaviour an
// older host does not have, and guessing in the provider's favour runs a
// connector against a contract it already said it cannot work with.
//
// This is deliberately the reverse of the cerr default, where an unclassified
// failure is [cerr.KindUnknown] and escalates nothing. Both are correct, and
// the cerr package documents the same asymmetry from its own side: an
// unverifiable connector must not run, while an unclassified transient failure
// must not escalate. Do not unify them.
//
// A version is MAJOR.MINOR.PATCH, three non-negative integers. Anything else
// is unparseable and therefore refused, which is why this accepts no
// pre-release or build suffix: a comparison this gate cannot make
// unambiguously is one it must not make at all.
func (d Doc) RequireHost(hostVersion string) error {
	want, err := parseVersion(d.MinHostVersion)
	if err != nil {
		return fmt.Errorf(
			"manifest: connector %q declares an unusable min_host_version: %w; a version gate that cannot be "+
				"evaluated is refused rather than assumed satisfied — the provider itself is asserting it needs a "+
				"host it can name", d.Name, err)
	}
	got, err := parseVersion(hostVersion)
	if err != nil {
		return fmt.Errorf(
			"manifest: this host cannot state its own contract version: %w; a host that cannot establish it is new "+
				"enough for connector %q must not dial it", err, d.Name)
	}
	if compareVersion(got, want) < 0 {
		return fmt.Errorf(
			"manifest: connector %q requires a host at min_host_version %s, and this host is %s; the connector is "+
				"refused rather than dialled, because it has already said this host lacks behaviour it depends on",
			d.Name, d.MinHostVersion, hostVersion)
	}
	return nil
}

// parseVersion splits a MAJOR.MINOR.PATCH version into its three numbers. It
// is strict on purpose — see [Doc.RequireHost] for why an unparseable version
// is refused rather than tolerated.
func parseVersion(v string) ([3]int, error) {
	var out [3]int
	if v == "" {
		return out, fmt.Errorf("version is empty")
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("version %q is not MAJOR.MINOR.PATCH", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return out, fmt.Errorf("version %q has a non-numeric or non-canonical component %q", v, p)
		}
		out[i] = n
	}
	return out, nil
}

// compareVersion orders two parsed versions: -1 if a is older than b, 0 if
// they are equal, +1 if a is newer.
func compareVersion(a, b [3]int) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

// validateCapabilities rejects any declared capability outside the closed
// vocabulary of the class in Implements. A class with no registered
// vocabulary is passed through — see classCapabilities for why.
func (d Doc) validateCapabilities() error {
	known, registered := classCapabilities[d.Implements]
	if !registered {
		return nil
	}
	var unknown []string
	for _, c := range d.Capabilities {
		if !known.Has(c) {
			unknown = append(unknown, strconv.Quote(string(c)))
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf(
		"manifest: capabilit(ies) %s are not in the %s vocabulary %s; "+
			"a host ignores a capability it does not recognise, so a misspelling silently becomes an undeclared capability",
		strings.Join(unknown, ", "), d.Implements, known)
}

// validateAuthEntry enforces the refs-only invariant: an Auth value must be
// either a well-formed op:// secret reference or a bare environment-variable
// NAME — the name of a variable to read at runtime, never the secret itself.
func validateAuthEntry(v string) error {
	if strings.HasPrefix(v, "op://") {
		if _, err := ref.Parse(v); err != nil {
			return fmt.Errorf("invalid ref: %w", err)
		}
		return nil
	}
	if envNameRE.MatchString(v) {
		return nil
	}
	return fmt.Errorf("must be an op:// ref or an ENV_NAME, not literal material: %q", v)
}
