package credconform_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credconform"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// Metavariable-form references. No estate identifiers: these are shapes, not
// addresses of anything.
const (
	refPresentA = "op://<vault>/<item>/<field>"
	refPresentB = "op://<vault>/<other-item>/<field>"
	refMissing  = "op://<vault>/<missing-item>/<field>"
)

func mustRef(t *testing.T, s string) ref.Ref {
	t.Helper()
	r, err := ref.Parse(s)
	if err != nil {
		t.Fatalf("ref.Parse(%q): %v", s, err)
	}
	return r
}

func opts(t *testing.T, m manifest.Doc) credconform.Options {
	t.Helper()
	return credconform.Options{
		Manifest:     m,
		Resolvable:   []ref.Ref{mustRef(t, refPresentA), mustRef(t, refPresentB)},
		Unresolvable: mustRef(t, refMissing),
	}
}

// ---------------------------------------------------------------------------
// The compliant baseline. Every non-compliant fixture below is this connector
// with ONE property broken, so a failing check is attributable to that
// property and not to the fixture being generally broken.
// ---------------------------------------------------------------------------

type baseLoader struct {
	caps connector.Capabilities
}

func (l *baseLoader) vals() map[string]string {
	return map[string]string{
		refPresentA: "placeholder-value-a",
		refPresentB: "placeholder-value-b",
	}
}

func (l *baseLoader) Resolve(_ context.Context, r ref.Ref) (credential.Resolution, error) {
	v, ok := l.vals()[r.String()]
	if !ok {
		return credential.Resolution{}, cerr.New(cerr.KindNotFound, "Resolve", nil)
	}
	return credential.Resolved(credential.NewMaterial([]byte(v)))
}

func (l *baseLoader) List(context.Context, string) ([]ref.Ref, error) {
	return nil, cerr.New(cerr.KindUnsupported, "List", nil)
}

func (l *baseLoader) Lease(context.Context, ref.Ref) (credential.Resolution, credential.Lease, error) {
	return credential.Resolution{}, credential.Lease{}, cerr.New(cerr.KindUnsupported, "Lease", nil)
}

func (l *baseLoader) Renew(context.Context, credential.Lease) (credential.Lease, error) {
	return credential.Lease{}, cerr.New(cerr.KindUnsupported, "Renew", nil)
}

func (l *baseLoader) Revoke(context.Context, credential.Lease) error {
	return cerr.New(cerr.KindUnsupported, "Revoke", nil)
}

func (l *baseLoader) Implements() connector.Class { return connector.ClassCredentialLoader }

func (l *baseLoader) Capabilities() connector.Capabilities {
	if l.caps == nil {
		return connector.Capabilities{credential.CapRead}
	}
	return l.caps
}

func (l *baseLoader) Health(context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}

func (l *baseLoader) Close() error { return nil }

var _ credential.CredentialLoader = (*baseLoader)(nil)

// batchLoader is the compliant batch-capable connector: it declares
// CapBatchResolve, implements credential.BatchResolver, and returns one
// result per requested ref, in order.
type batchLoader struct{ baseLoader }

func (l *batchLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapBatchResolve}
}

func (l *batchLoader) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	out := make([]credential.BatchResult, 0, len(refs))
	for _, r := range refs {
		res, err := l.Resolve(ctx, r)
		out = append(out, credential.BatchResult{Ref: r, Resolution: res, Err: err})
	}
	return out, nil
}

var _ credential.BatchResolver = (*batchLoader)(nil)

func manifestFor(caps ...connector.Capability) manifest.Doc {
	return manifest.Doc{
		Name:         "placeholder-credential-loader",
		Implements:   connector.ClassCredentialLoader,
		Kind:         manifest.KindNative,
		Capabilities: caps,
	}
}

// ---------------------------------------------------------------------------
// The deliberately NON-COMPLIANT connectors. Each one violates exactly one
// obligation, and each exists to prove the corresponding check bites. A
// harness only ever run against compliant input proves nothing.
// ---------------------------------------------------------------------------

// signedOutLoader is REQ-ARQ-P-17's failure, modelled on the real backend
// behaviour that produces it: a signed-out read returns EMPTY OUTPUT WITH
// EXIT CODE 0, and this connector forwards that faithfully as a success
// carrying nothing.
type signedOutLoader struct{ baseLoader }

func (l *signedOutLoader) Resolve(context.Context, ref.Ref) (credential.Resolution, error) {
	return credential.Resolution{}, nil // empty output, exit code 0
}

// vendorTextLoader is REQ-ARQ-P-19's failure: it fails with the backend's own
// prose, untyped, leaving the host nothing to act on but the message —
// which is precisely the string-matching the typed vocabulary replaces.
type vendorTextLoader struct{ baseLoader }

func (l *vendorTextLoader) Resolve(ctx context.Context, r ref.Ref) (credential.Resolution, error) {
	if _, ok := l.vals()[r.String()]; !ok {
		return credential.Resolution{}, errors.New("could not read item: rate-limited, please try again later")
	}
	return l.baseLoader.Resolve(ctx, r)
}

// batchLiarLoader is REQ-ARQ-P-20's failure: the manifest declares batch
// resolution and the connector does not implement it. The host plans one
// call and finds the operation is not there.
type batchLiarLoader struct{ baseLoader }

func (l *batchLiarLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapBatchResolve}
}

// misalignedBatchLoader implements batch, but returns results that do not
// correspond to the request — here, one short. A host that trusts position
// hands the wrong secret to the wrong caller.
type misalignedBatchLoader struct{ batchLoader }

func (l *misalignedBatchLoader) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	full, err := l.batchLoader.ResolveBatch(ctx, refs)
	if err != nil || len(full) == 0 {
		return full, err
	}
	return full[:len(full)-1], nil
}

// emptyBatchElementLoader implements batch and returns the right number of
// results in the right order — but one of them carries neither a value nor a
// failure. It is REQ-ARQ-P-17's hole, reopened one level down.
type emptyBatchElementLoader struct{ batchLoader }

func (l *emptyBatchElementLoader) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	full, err := l.batchLoader.ResolveBatch(ctx, refs)
	if err != nil || len(full) == 0 {
		return full, err
	}
	full[0] = credential.BatchResult{Ref: full[0].Ref}
	return full, nil
}

// brokenBackendLoader fails on a reference this run declares resolvable. It
// is not a contract violation — the failure is typed — but the run cannot
// conclude anything about no-empty-success from a connector that resolves
// nothing, so it must not report a pass.
type brokenBackendLoader struct{ baseLoader }

func (l *brokenBackendLoader) Resolve(context.Context, ref.Ref) (credential.Resolution, error) {
	return credential.Resolution{}, cerr.New(cerr.KindUnavailable, "Resolve", nil)
}

// resolvesAnythingLoader resolves every reference, including one the run
// declares unresolvable. A connector that never fails leaves its failure
// classification untested, which is how a harness comes back green having
// checked nothing.
type resolvesAnythingLoader struct{ baseLoader }

func (l *resolvesAnythingLoader) Resolve(context.Context, ref.Ref) (credential.Resolution, error) {
	return credential.Resolved(credential.NewMaterial([]byte("placeholder-value")))
}

// selfAccusingLoader classifies its own failure as a contract violation —
// the kind a HOST reports about a connector, not one a connector returns. It
// would let a broken connector present itself as an already-diagnosed one.
type selfAccusingLoader struct{ baseLoader }

func (l *selfAccusingLoader) Resolve(ctx context.Context, r ref.Ref) (credential.Resolution, error) {
	if _, ok := l.vals()[r.String()]; !ok {
		return credential.Resolution{}, cerr.New(cerr.KindContractViolation, "Resolve", nil)
	}
	return l.baseLoader.Resolve(ctx, r)
}

// halfBatchingLoader implements batch and resolves single references, but its
// batch path disagrees with its single path: a reference that resolves one at
// a time fails in the batch. A host gets a different answer depending on
// which one it called.
type halfBatchingLoader struct{ batchLoader }

func (l *halfBatchingLoader) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	out, err := l.batchLoader.ResolveBatch(ctx, refs)
	if err != nil || len(out) == 0 {
		return out, err
	}
	out[0] = credential.BatchResult{Ref: out[0].Ref, Err: cerr.New(cerr.KindNotFound, "ResolveBatch", nil)}
	return out, nil
}

// unbatchedBatcherLoader implements batch and declares it nowhere at all.
type unbatchedBatcherLoader struct{ batchLoader }

func (l *unbatchedBatcherLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead}
}

// wholeBatchFailingLoader fails the batch call outright.
type wholeBatchFailingLoader struct{ batchLoader }

func (l *wholeBatchFailingLoader) ResolveBatch(context.Context, []ref.Ref) ([]credential.BatchResult, error) {
	return nil, cerr.New(cerr.KindUnavailable, "ResolveBatch", nil)
}

// ---------------------------------------------------------------------------

func TestNonCompliantConnectorsFailTheCheckTheyViolate(t *testing.T) {
	batchManifest := manifestFor(credential.CapRead, credential.CapBatchResolve)

	for _, tc := range []struct {
		name     string
		loader   credential.CredentialLoader
		manifest manifest.Doc
		wantFail string
	}{
		{
			name:     "success carrying no value (REQ-ARQ-P-17)",
			loader:   &signedOutLoader{},
			manifest: manifestFor(credential.CapRead),
			wantFail: credconform.CheckResolveNoEmptySuccess,
		},
		{
			name:     "untyped, vendor-text failure (REQ-ARQ-P-19)",
			loader:   &vendorTextLoader{},
			manifest: manifestFor(credential.CapRead),
			wantFail: credconform.CheckFailureTyped,
		},
		{
			name:     "manifest declares batch, connector does not implement it (REQ-ARQ-P-20)",
			loader:   &batchLiarLoader{},
			manifest: batchManifest,
			wantFail: credconform.CheckBatchDeclared,
		},
		{
			name:     "batch results do not correspond to the request",
			loader:   &misalignedBatchLoader{},
			manifest: batchManifest,
			wantFail: credconform.CheckBatchShape,
		},
		{
			name:     "a batch element carries neither value nor failure",
			loader:   &emptyBatchElementLoader{},
			manifest: batchManifest,
			wantFail: credconform.CheckBatchShape,
		},
		{
			name:     "fails on a reference the run declares resolvable",
			loader:   &brokenBackendLoader{},
			manifest: manifestFor(credential.CapRead),
			wantFail: credconform.CheckResolveNoEmptySuccess,
		},
		{
			name:     "resolves a reference the run declares unresolvable",
			loader:   &resolvesAnythingLoader{},
			manifest: manifestFor(credential.CapRead),
			wantFail: credconform.CheckFailureTyped,
		},
		{
			name:     "classifies its own failure as a contract violation",
			loader:   &selfAccusingLoader{},
			manifest: manifestFor(credential.CapRead),
			wantFail: credconform.CheckFailureTyped,
		},
		{
			name:     "batch disagrees with single resolution",
			loader:   &halfBatchingLoader{},
			manifest: batchManifest,
			wantFail: credconform.CheckBatchShape,
		},
		{
			name:     "the whole batch call fails",
			loader:   &wholeBatchFailingLoader{},
			manifest: batchManifest,
			wantFail: credconform.CheckBatchShape,
		},
		{
			name:     "implements batch and declares it nowhere",
			loader:   &unbatchedBatcherLoader{},
			manifest: manifestFor(credential.CapRead),
			wantFail: credconform.CheckBatchDeclared,
		},
		{
			name:     "manifest is invalid",
			loader:   &baseLoader{},
			manifest: manifest.Doc{Implements: connector.ClassCredentialLoader, Kind: manifest.KindNative},
			wantFail: credconform.CheckManifest,
		},
		{
			name:   "manifest is for another connector class",
			loader: &baseLoader{},
			manifest: manifest.Doc{
				Name: "placeholder-credential-loader", Kind: manifest.KindNative,
				Implements: connector.Class("SomethingElse"), Capabilities: []connector.Capability{credential.CapRead},
			},
			wantFail: credconform.CheckManifest,
		},
		{
			name:     "manifest declares a capability outside the vocabulary",
			loader:   &baseLoader{},
			manifest: manifestFor(credential.CapRead, connector.Capability("btach_resolve")),
			wantFail: credconform.CheckManifest,
		},
		{
			name:     "manifest and running connector disagree about capabilities",
			loader:   &baseLoader{caps: connector.Capabilities{credential.CapRead, credential.CapLease}},
			manifest: manifestFor(credential.CapRead),
			wantFail: credconform.CheckCapabilityHonesty,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := credconform.Run(context.Background(), tc.loader, opts(t, tc.manifest))
			if err != nil {
				t.Fatalf("the harness could not run: %v", err)
			}
			if rep.OK() {
				t.Fatalf("a deliberately non-compliant connector passed conformance:\n%s", rep)
			}
			if !failed(rep, tc.wantFail) {
				t.Fatalf("check %q did not fail; the harness caught something else, or nothing that names this property:\n%s", tc.wantFail, rep)
			}
			if rep.Err() == nil {
				t.Fatalf("Report.Err() must be non-nil when a check failed")
			}
			if cerr.KindOf(rep.Err()) != cerr.KindInvalid {
				t.Fatalf("Report.Err() Kind = %v, want KindInvalid", cerr.KindOf(rep.Err()))
			}
		})
	}
}

// TestBatchImplementedButUndeclaredFails is the converse false declaration:
// the operation exists and the manifest does not say so, which leaves a host
// resolving one reference at a time against a connector that could have done
// it in one call.
func TestBatchImplementedButUndeclaredFails(t *testing.T) {
	rep, err := credconform.Run(context.Background(), &batchLoader{}, opts(t, manifestFor(credential.CapRead)))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	if !failed(rep, credconform.CheckBatchDeclared) {
		t.Fatalf("an implemented-but-undeclared batch must fail %q:\n%s", credconform.CheckBatchDeclared, rep)
	}
}

// TestCompliantConnectorsPass is the control. Without it, a harness that
// failed everything would look just as convincing as one that works.
func TestCompliantConnectorsPass(t *testing.T) {
	t.Run("no batch capability", func(t *testing.T) {
		rep, err := credconform.Run(context.Background(), &baseLoader{}, opts(t, manifestFor(credential.CapRead)))
		if err != nil {
			t.Fatalf("the harness could not run: %v", err)
		}
		if !rep.OK() {
			t.Fatalf("a compliant connector must pass:\n%s", rep)
		}
		// The batch shape check is not reported for a connector that neither
		// declares nor implements batch — but it must not be the reason the
		// report is green.
		if ran(rep, credconform.CheckBatchShape) {
			t.Fatalf("batch shape should not be reported for a non-batch connector:\n%s", rep)
		}
	})

	t.Run("batch capability", func(t *testing.T) {
		m := manifestFor(credential.CapRead, credential.CapBatchResolve)
		rep, err := credconform.Run(context.Background(), &batchLoader{}, opts(t, m))
		if err != nil {
			t.Fatalf("the harness could not run: %v", err)
		}
		if !rep.OK() {
			t.Fatalf("a compliant batch connector must pass:\n%s", rep)
		}
		if !ran(rep, credconform.CheckBatchShape) {
			t.Fatalf("batch shape must be checked for a batch connector:\n%s", rep)
		}
	})
}

// TestEveryObligationIsChecked pins the check set. A check that stops running
// is a hole in the harness, and a report that is green because nothing looked
// is the failure mode this asserts against.
func TestEveryObligationIsChecked(t *testing.T) {
	rep, err := credconform.Run(context.Background(), &batchLoader{}, opts(t, manifestFor(credential.CapRead, credential.CapBatchResolve)))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	for _, name := range []string{
		credconform.CheckManifest,
		credconform.CheckCapabilityHonesty,
		credconform.CheckBatchDeclared,
		credconform.CheckResolveNoEmptySuccess,
		credconform.CheckFailureTyped,
		credconform.CheckBatchShape,
	} {
		if !ran(rep, name) {
			t.Fatalf("check %q did not run:\n%s", name, rep)
		}
	}
}

// TestRunRefusesToPretend: without fixtures there is nothing to drive the
// connector with, and a harness that reports success in that state is worse
// than one that refuses.
func TestRunRefusesToPretend(t *testing.T) {
	valid := opts(t, manifestFor(credential.CapRead))

	t.Run("nil connector", func(t *testing.T) {
		if _, err := credconform.Run(context.Background(), nil, valid); err == nil {
			t.Fatalf("Run(nil connector) must error")
		}
	})
	t.Run("no resolvable references", func(t *testing.T) {
		o := valid
		o.Resolvable = nil
		if _, err := credconform.Run(context.Background(), &baseLoader{}, o); err == nil {
			t.Fatalf("Run without a resolvable reference must error rather than report a green run")
		}
	})
	t.Run("no unresolvable reference", func(t *testing.T) {
		o := valid
		o.Unresolvable = ref.Ref{}
		if _, err := credconform.Run(context.Background(), &baseLoader{}, o); err == nil {
			t.Fatalf("Run without an unresolvable reference cannot check typed failure, and must say so")
		}
	})
}

// TestBothDirectionsOfCapabilityDisagreementAreReported: a report that names
// only one side of a mismatch sends an author round the loop twice.
func TestBothDirectionsOfCapabilityDisagreementAreReported(t *testing.T) {
	loader := &baseLoader{caps: connector.Capabilities{credential.CapRead, credential.CapOIDC}}
	rep, err := credconform.Run(context.Background(), loader, opts(t, manifestFor(credential.CapRead, credential.CapLease)))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	detail := detailOf(rep, credconform.CheckCapabilityHonesty)
	if !strings.Contains(detail, string(credential.CapLease)) || !strings.Contains(detail, string(credential.CapOIDC)) {
		t.Fatalf("both sides of the mismatch must be named, got: %s", detail)
	}
}

func TestReportStringNamesFailedChecks(t *testing.T) {
	rep, err := credconform.Run(context.Background(), &signedOutLoader{}, opts(t, manifestFor(credential.CapRead)))
	if err != nil {
		t.Fatalf("the harness could not run: %v", err)
	}
	s := rep.String()
	if !strings.Contains(s, credconform.CheckResolveNoEmptySuccess) || !strings.Contains(s, "FAIL") {
		t.Fatalf("Report.String() must name the failed check for a CI log:\n%s", s)
	}
	if !strings.Contains(rep.Err().Error(), "placeholder-credential-loader") {
		t.Fatalf("the failure must name the connector: %v", rep.Err())
	}
}

func detailOf(rep credconform.Report, name string) string {
	for _, r := range rep.Results {
		if r.Name == name {
			return r.Detail
		}
	}
	return ""
}

func failed(rep credconform.Report, name string) bool {
	return slices.ContainsFunc(rep.Failures(), func(r credconform.Result) bool { return r.Name == name })
}

func ran(rep credconform.Report, name string) bool {
	return slices.ContainsFunc(rep.Results, func(r credconform.Result) bool { return r.Name == name })
}
