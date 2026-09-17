package codehost

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

const (
	oidA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oidB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	oidC = "cccccccccccccccccccccccccccccccccccccccc"
)

func TestKnownCapabilities_IncludesExactRef(t *testing.T) {
	if !KnownCapabilities().Has(CapExactRef) {
		t.Fatalf("KnownCapabilities=%v omits %s; existing connectors stay compatible only if this remains optional", KnownCapabilities(), CapExactRef)
	}
}

func TestExactRefTransport_IsNotAMethodOnTheRequiredInterface(t *testing.T) {
	required := reflect.TypeOf((*CodeHost)(nil)).Elem()
	for _, name := range []string{"ReadExact", "CompareAndSwap"} {
		if _, found := required.MethodByName(name); found {
			t.Fatalf("CodeHost declares %s: the optional transport was added to the required set", name)
		}
	}
	tier := reflect.TypeOf((*ExactRefTransport)(nil)).Elem()
	if tier.NumMethod() != 2 {
		t.Fatalf("ExactRefTransport declares %d methods, want 2 (read and CAS are one safety surface)", tier.NumMethod())
	}
}

func TestExactReadRequest_Validate_ClassifiesMissingDeniedMalformedAndRedirect(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  ExactReadRequest
		kind cerr.Kind
	}{
		{"missing realm", ExactReadRequest{NativeID: "42", Ref: "refs/heads/main"}, cerr.KindInvalid},
		{"missing native id", ExactReadRequest{Realm: "forge", Ref: "refs/heads/main"}, cerr.KindInvalid},
		{"path native id", ExactReadRequest{Realm: "forge", NativeID: "owner/repo", Ref: "refs/heads/main"}, cerr.KindInvalid},
		{"missing ref", ExactReadRequest{Realm: "forge", NativeID: "42"}, cerr.KindInvalid},
	} {
		err := tc.req.Validate()
		if err == nil || cerr.KindOf(err) != tc.kind {
			t.Fatalf("%s: err=%v kind=%v want %v", tc.name, err, cerr.KindOf(err), tc.kind)
		}
	}
}

func TestRequestTypes_CarryNoCredentialCommandOrUntypedMap(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(ExactReadRequest{}),
		reflect.TypeOf(CASRequest{}),
		reflect.TypeOf(CASReceipt{}),
		reflect.TypeOf(ExactReadResult{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			n := strings.ToLower(f.Name)
			if strings.Contains(n, "token") || strings.Contains(n, "secret") || strings.Contains(n, "password") || strings.Contains(n, "command") || f.Type.Kind() == reflect.Map {
				t.Errorf("%s.%s is credential, command, or untyped map material", typ.Name(), f.Name)
			}
		}
	}
}

func TestMemTransport_CompareAndSwap_AppliedMismatchIndeterminateCancellation(t *testing.T) {
	ctx := t.Context()
	m := newMemTransport()
	req := CASRequest{Realm: "forge", NativeID: "42", Ref: "refs/arqtos/reservations", New: ObjectID(oidA)}
	got, err := m.CompareAndSwap(ctx, req)
	if err != nil || got.Outcome != CASApplied || got.New != ObjectID(oidA) {
		t.Fatalf("create: %+v %v", got, err)
	}
	stale, err := m.CompareAndSwap(ctx, CASRequest{Realm: "forge", NativeID: "42", Ref: req.Ref, Expected: ObjectID(oidB), New: ObjectID(oidC)})
	if err != nil || stale.Outcome != CASMismatch || stale.Observed != ObjectID(oidA) {
		t.Fatalf("stale: %+v %v", stale, err)
	}
	applied, err := m.CompareAndSwap(ctx, CASRequest{Realm: "forge", NativeID: "42", Ref: req.Ref, Expected: ObjectID(oidA), New: ObjectID(oidB)})
	if err != nil || applied.Outcome != CASApplied {
		t.Fatalf("swap: %+v %v", applied, err)
	}
	m.indeterminate = true
	unk, err := m.CompareAndSwap(ctx, CASRequest{Realm: "forge", NativeID: "42", Ref: req.Ref, Expected: ObjectID(oidB), New: ObjectID(oidC)})
	if err != nil || unk.Outcome != CASIndeterminate {
		t.Fatalf("indeterminate: %+v %v", unk, err)
	}
	head, err := m.ReadExact(ctx, ExactReadRequest{Realm: "forge", NativeID: "42", Ref: req.Ref})
	if err != nil || head.ObjectID != ObjectID(oidB) {
		t.Fatalf("readback after indeterminate must not invent success: %+v %v", head, err)
	}
	cancelled, err := m.CompareAndSwap(cancelledCtx(t), req)
	if err == nil || cerr.KindOf(err) != cerr.KindTimeout || cancelled.Outcome == CASApplied {
		t.Fatalf("cancellation became a write result: %+v %v", cancelled, err)
	}
}

func TestMemTransport_ReadExact_ClassifiesDeniedRedirectAndMismatch(t *testing.T) {
	ctx := t.Context()
	m := newMemTransport()
	_, _ = m.CompareAndSwap(ctx, CASRequest{Realm: "forge", NativeID: "42", Ref: "refs/heads/main", New: ObjectID(oidA)})
	m.denied = true
	_, err := m.ReadExact(ctx, ExactReadRequest{Realm: "forge", NativeID: "42", Ref: "refs/heads/main"})
	if cerr.KindOf(err) != cerr.KindUnauthorized {
		t.Fatalf("denied: %v", err)
	}
	m.denied = false
	m.redirect = true
	_, err = m.ReadExact(ctx, ExactReadRequest{Realm: "forge", NativeID: "42", Ref: "refs/heads/main"})
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("redirect: %v", err)
	}
	m.redirect = false
	_, err = m.ReadExact(ctx, ExactReadRequest{Realm: "other", NativeID: "42", Ref: "refs/heads/main"})
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("realm mismatch: %v", err)
	}
	_, err = m.ReadExact(ctx, ExactReadRequest{Realm: "forge", NativeID: "99", Ref: "refs/heads/main"})
	if cerr.KindOf(err) != cerr.KindNotFound {
		t.Fatalf("missing: %v", err)
	}
}

type memTransport struct {
	heads         map[string]ObjectID
	indeterminate bool
	denied        bool
	redirect      bool
}

func newMemTransport() *memTransport {
	return &memTransport{heads: map[string]ObjectID{}}
}

func (m *memTransport) ReadExact(ctx context.Context, req ExactReadRequest) (ExactReadResult, error) {
	return ExactReadResult{}, cerr.New(cerr.KindUnsupported, "ReadExact", errExactRefUnimplemented)
}

func (m *memTransport) CompareAndSwap(ctx context.Context, req CASRequest) (CASReceipt, error) {
	return CASReceipt{}, cerr.New(cerr.KindUnsupported, "CompareAndSwap", errExactRefUnimplemented)
}

func cancelledCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

var _ ExactRefTransport = (*memTransport)(nil)
var _ connector.Capability = CapExactRef
