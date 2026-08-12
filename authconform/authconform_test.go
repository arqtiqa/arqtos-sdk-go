package authconform_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/authconform"
	"github.com/arqtiqa/arqtos-sdk-go/authenticator"
	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

// The fixture values every stub and every run in this file agrees on.
const (
	validCode    = "code-valid"
	rejectedCode = "code-rejected"
	inactiveCode = "code-inactive"
	unknownHdl   = "handle-never-issued"
	principalID  = "00u-synthetic"
	connName     = "synthetic-idp"
)

// stub is a compliant Authenticator, with one knob per check so a violating
// variant is a one-field change rather than a second implementation. A second
// implementation would drift from this one, and the drift would look like a
// harness bug.
type stub struct {
	class connector.Class
	caps  connector.Capabilities

	noURL            bool // Begin returns no authorization URL
	noHandle         bool // Begin returns no handle
	incoherent       bool // Complete returns Authenticated with no PrincipalID
	anonOnReject     bool // a rejected code yields (anonymous assertion, nil error)
	unclassifiedFail bool // failures are bare errors rather than cerr-classified
	// acceptsUnknownHandle accepts the UNKNOWN-handle fixture specifically,
	// rather than any handle at all.
	//
	// ⚠️ The broader knob was the first version and the companion meta-test
	// refused it: a stub accepting ANY handle also accepts a REUSED one, so it
	// broke handle/is-single-use as well and neither catch could be
	// attributed. "Accepts anything" is a superset of "accepts a replay", and a
	// violating stub has to be narrower than the property it violates.
	acceptsUnknownHandle bool
	inactiveAsError      bool // a verified-but-inactive principal is reported as an error
	wrongPrincipal       bool // a coherent assertion about somebody else
	reusableHandles      bool // a handle survives the exchange it completes
	unhealthy            bool // Health returns a status outside the vocabulary

	// issued holds LIVE handles. A handle is consumed by Complete, because
	// single-use is the whole point of binding a verifier and a nonce to it —
	// and a stub that let one be reused could not catch a harness that reuses
	// one.
	issued map[string]bool
	next   int
}

func newStub() *stub {
	return &stub{
		class:  connector.ClassAuthenticator,
		caps:   connector.Capabilities{},
		issued: map[string]bool{},
	}
}

func (s *stub) Implements() connector.Class          { return s.class }
func (s *stub) Capabilities() connector.Capabilities { return s.caps }
func (s *stub) Close() error                         { return nil }

func (s *stub) Health(context.Context) (connector.Health, error) {
	if s.unhealthy {
		return connector.Health{Status: connector.HealthStatus(99)}, nil
	}
	return connector.Health{Status: connector.Healthy}, nil
}

func (s *stub) Begin(context.Context) (authenticator.Challenge, error) {
	s.next++
	c := authenticator.Challenge{
		AuthorizationURL: "https://idp.example/authorize",
		Handle:           fmt.Sprintf("h-%d", s.next),
	}
	if s.noURL {
		c.AuthorizationURL = ""
	}
	if s.noHandle {
		c.Handle = ""
	}
	s.issued[c.Handle] = true
	return c, nil
}

func (s *stub) Complete(_ context.Context, handle, code string) (authenticator.Assertion, error) {
	if !s.issued[handle] && !(s.acceptsUnknownHandle && handle == unknownHdl) {
		if s.unclassifiedFail {
			return authenticator.Assertion{}, errors.New("no such handle")
		}
		return authenticator.Assertion{}, cerr.New(cerr.KindInvalid, "Complete", errors.New("unknown handle"))
	}
	if !s.reusableHandles {
		delete(s.issued, handle) // consumed, whatever the outcome
	}
	switch code {
	case rejectedCode:
		if s.anonOnReject {
			return authenticator.Assertion{}, nil
		}
		if s.unclassifiedFail {
			return authenticator.Assertion{}, errors.New("rejected")
		}
		return authenticator.Assertion{}, &authenticator.VerificationError{
			Failure: authenticator.VerificationBadSignature,
		}
	case inactiveCode:
		if s.inactiveAsError {
			return authenticator.Assertion{}, cerr.New(cerr.KindUnauthorized, "Complete", errors.New("disabled"))
		}
		return authenticator.Assertion{Authenticated: true, PrincipalID: principalID, Active: false}, nil
	default:
		if s.incoherent {
			// Incoherent in the shape ONLY the coherence assertion catches: a
			// principal named while denying authentication, where the name is
			// the one the fixtures expect.
			//
			// ⚠️ The obvious stub — Authenticated true with no PrincipalID — is
			// caught by the coherence assertion AND by the identity assertion
			// beside it, so removing the coherence assertion left the check
			// still failing and the meta-gate still green. Measured: mutating
			// that assertion away did not go red until this stub was narrowed.
			// A stub caught by two conditions proves neither.
			return authenticator.Assertion{Authenticated: false, PrincipalID: principalID}, nil
		}
		if s.wrongPrincipal {
			return authenticator.Assertion{Authenticated: true, PrincipalID: "00u-somebody-else", Active: true}, nil
		}
		return authenticator.Assertion{Authenticated: true, PrincipalID: principalID, Active: true}, nil
	}
}

func goodManifest() manifest.Doc {
	return manifest.Doc{Name: connName, Implements: connector.ClassAuthenticator, Kind: manifest.KindNative}
}

func opts(m manifest.Doc) authconform.Options {
	return authconform.Options{
		Manifest:            m,
		ValidCode:           validCode,
		RejectedCode:        rejectedCode,
		InactiveCode:        inactiveCode,
		UnknownHandle:       unknownHdl,
		ExpectedPrincipalID: principalID,
	}
}

func run(t *testing.T, a authenticator.Authenticator, m manifest.Doc) authconform.Report {
	t.Helper()
	rep, err := authconform.Run(context.Background(), a, opts(m))
	if err != nil {
		t.Fatalf("Run could not be carried out: %v", err)
	}
	if len(rep.Results) == 0 {
		t.Fatal("Run produced no results; an empty report passes every assertion built on it")
	}
	return rep
}

func requireFailed(t *testing.T, rep authconform.Report, name string) {
	t.Helper()
	for _, r := range rep.Failures() {
		if r.Name == name {
			if r.Detail == "" {
				t.Errorf("check %s failed with no detail; a reviewer cannot act on a bare failure", name)
			}
			return
		}
	}
	t.Fatalf("check %s did not fail; report was:\n%s", name, rep)
}

func TestRun_CompliantStubIsGreenOnEveryCheck(t *testing.T) {
	rep := run(t, newStub(), goodManifest())
	if !rep.OK() {
		t.Fatalf("a compliant connector failed conformance:\n%s", rep)
	}
	if rep.Err() != nil {
		t.Fatalf("Err() is non-nil for a passing report: %v", rep.Err())
	}
}

func TestRun_RefusesANilConnector(t *testing.T) {
	if _, err := authconform.Run(context.Background(), nil, opts(goodManifest())); err == nil {
		t.Fatal("a nil connector was accepted")
	}
}

// TestRun_RefusesAnIncompleteFixtureSet: an optional fixture is a check that
// silently skips, and a check that skipped is reported by nothing.
func TestRun_RefusesAnIncompleteFixtureSet(t *testing.T) {
	for _, blank := range []struct {
		field string
		mut   func(*authconform.Options)
	}{
		{"ValidCode", func(o *authconform.Options) { o.ValidCode = "" }},
		{"RejectedCode", func(o *authconform.Options) { o.RejectedCode = "" }},
		{"InactiveCode", func(o *authconform.Options) { o.InactiveCode = "" }},
		{"UnknownHandle", func(o *authconform.Options) { o.UnknownHandle = "" }},
		{"ExpectedPrincipalID", func(o *authconform.Options) { o.ExpectedPrincipalID = "" }},
	} {
		t.Run(blank.field, func(t *testing.T) {
			o := opts(goodManifest())
			blank.mut(&o)
			if _, err := authconform.Run(context.Background(), newStub(), o); err == nil {
				t.Fatalf("a run with %s unset was carried out", blank.field)
			}
		})
	}
}

// TestRun_RefusesAnIncoherentFixtureSet: a fixture set can be COMPLETE and
// still describe an impossible directory. Every pair below would let a check
// observe something other than the property it names.
func TestRun_RefusesAnIncoherentFixtureSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*authconform.Options)
	}{
		{"valid and rejected are the same code", func(o *authconform.Options) { o.RejectedCode = o.ValidCode }},
		{"valid and inactive are the same code", func(o *authconform.Options) { o.InactiveCode = o.ValidCode }},
		{"rejected and inactive are the same code", func(o *authconform.Options) { o.InactiveCode = o.RejectedCode }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := opts(goodManifest())
			tc.mut(&o)
			if _, err := authconform.Run(context.Background(), newStub(), o); err == nil {
				t.Fatalf("an incoherent fixture set was accepted: %s", tc.name)
			}
		})
	}
}

// violators maps each check name to a connector built to break exactly that
// check. The map is the whole point of this suite: a check never seen fail is
// not a check.
var violators = map[string]func() (authenticator.Authenticator, manifest.Doc){
	// ⚠️ Breaking the manifest via an invented CAPABILITY was the obvious
	// choice and it is wrong: it also fails capability/manifest-matches-runtime,
	// because the runtime then reports a capability the manifest does not — and
	// a stub that breaks two checks cannot attribute a catch to either. The
	// companion test caught it. An invalid Kind is a manifest-shape violation
	// that touches nothing else.
	authconform.CheckManifest: func() (authenticator.Authenticator, manifest.Doc) {
		m := goodManifest()
		m.Kind = manifest.Kind("not-a-runtime-kind")
		return newStub(), m
	},
	authconform.CheckClass: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.class = connector.ClassRoster
		return s, goodManifest()
	},
	authconform.CheckCapabilityHonesty: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.caps = connector.Capabilities{"ci_control"}
		return s, goodManifest()
	},
	authconform.CheckHealth: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.unhealthy = true
		return s, goodManifest()
	},
	authconform.CheckChallengeComplete: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.noURL = true
		return s, goodManifest()
	},
	authconform.CheckAssertionCoherent: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.incoherent = true
		return s, goodManifest()
	},
	authconform.CheckAssertionPrincipal: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.wrongPrincipal = true
		return s, goodManifest()
	},
	authconform.CheckRejectionIsTyped: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.anonOnReject = true
		return s, goodManifest()
	},
	authconform.CheckUnknownHandleRefused: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.acceptsUnknownHandle = true
		return s, goodManifest()
	},
	authconform.CheckHandleSingleUse: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.reusableHandles = true
		return s, goodManifest()
	},
	authconform.CheckInactiveIsReported: func() (authenticator.Authenticator, manifest.Doc) {
		s := newStub()
		s.inactiveAsError = true
		return s, goodManifest()
	},
}

// checkConstants reads the Check* constants out of the harness source, so the
// meta-gate below measures what the package DECLARES rather than what this test
// file remembers. A hand-maintained list here would agree with itself.
func checkConstants(t *testing.T) []string {
	t.Helper()

	const src = "authconform.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}

	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != len(vs.Names) {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Check") {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: %s is not a string literal; this test reads the check set out of the "+
						"source and cannot evaluate an expression", src, name.Name)
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: %s has an unreadable literal %s: %v", src, name.Name, lit.Value, err)
				}
				out = append(out, val)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s: found no Check constants at all; every assertion built on this would pass by "+
			"observing nothing", src)
	}
	return out
}

// TestRun_EveryCheckHasAViolatingStub is the gate this harness ships WITH,
// rather than acquiring later.
//
// Three of the five harnesses in this SDK have a violating-input test per check
// and nothing that fails if one is deleted. That is the difference between a
// habit and a gate, and a guard nobody watched fail is not a guard.
func TestRun_EveryCheckHasAViolatingStub(t *testing.T) {
	declared := checkConstants(t)

	for _, name := range declared {
		build, ok := violators[name]
		if !ok {
			t.Errorf("check %q has no entry in violators: nothing in this suite has ever seen it fail, "+
				"so nothing shows it can", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			c, m := build()
			requireFailed(t, run(t, c, m), name)
		})
	}

	for name := range violators {
		if !slices.Contains(declared, name) {
			t.Errorf("violators carries %q, which is not a declared Check constant: it drives a check "+
				"that does not exist", name)
		}
	}
}

// TestRun_AViolatingStubBreaksOnlyItsOwnCheck holds the other half: a stub that
// also broke a neighbour would let a new check pass on damage another check
// already reports.
func TestRun_AViolatingStubBreaksOnlyItsOwnCheck(t *testing.T) {
	for name, build := range violators {
		t.Run(name, func(t *testing.T) {
			c, m := build()
			for _, f := range run(t, c, m).Failures() {
				if f.Name != name {
					t.Errorf("the stub built to violate %s also failed %s (%s); this table cannot "+
						"attribute a catch to a check when one stub breaks two", name, f.Name, f.Detail)
				}
			}
		})
	}
}

func TestReport_ErrIsClassifiedAndNamesEveryFailure(t *testing.T) {
	s := newStub()
	s.incoherent = true
	rep := run(t, s, goodManifest())

	err := rep.Err()
	if err == nil {
		t.Fatal("Err() is nil for a failing report")
	}
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Errorf("Err() classifies as %v, want KindInvalid — the connector ran, its behaviour is wrong",
			cerr.KindOf(err))
	}
	if !strings.Contains(err.Error(), connName) {
		t.Errorf("Err() does not name the connector: %v", err)
	}
	for _, f := range rep.Failures() {
		if !strings.Contains(err.Error(), f.Name) {
			t.Errorf("Err() omits failed check %q", f.Name)
		}
	}
}
