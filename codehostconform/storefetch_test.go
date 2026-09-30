package codehostconform_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
	"github.com/arqtiqa/arqtos-sdk-go/codehostconform"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

type storeStub struct {
	*stub
	fault string
}

func (s storeStub) FetchStore(ctx context.Context, r codehost.StoreFetchRequest) (codehost.StoreFetchResult, error) {
	var zero codehost.StoreFetchResult
	if ctx.Err() != nil {
		if s.fault == "cancel" {
			return zero, ctx.Err()
		}
		return zero, cerr.New(cerr.KindTimeout, "FetchStore", ctx.Err())
	}
	if err := r.Validate(); err != nil {
		if s.fault == "invalid" {
			return zero, nil
		}
		return zero, err
	}
	if r.NativeID == "denied" {
		if s.fault == "unauthorized" {
			return zero, cerr.New(cerr.KindUnavailable, "FetchStore", errors.New("masked as missing"))
		}
		return zero, cerr.New(cerr.KindUnauthorized, "FetchStore", errors.New("denied"))
	}
	x := codehost.StoreFetchResult(r)
	if s.fault == "receipt" {
		x.NativeID = "other"
	}
	return x, nil
}

func storeOptions(t *testing.T) codehostconform.Options {
	t.Helper()
	request := func() codehost.StoreFetchRequest {
		return codehost.StoreFetchRequest{Realm: "host-a", NativeID: "1", StoreDir: t.TempDir()}
	}
	denied := request()
	denied.NativeID = "denied"
	return codehostconform.Options{
		Manifest:                 stubManifest(codehost.CapStoreFetch),
		ListableOwner:            fixtureListable,
		UnlistableOwner:          fixtureUnlistable,
		UnreadableProtectionRepo: fixtureUnreadableRepo,
		UnreadableProtectionRef:  fixtureUnreadableRef,
		MissingOwner:             fixtureMissingOwner,
		MissingProtectionRepo:    fixtureMissingRepo,
		MissingProtectionRef:     fixtureMissingRef,
		StoreFetch: &codehostconform.StoreFetchFixtures{
			Request:         request(),
			DeniedRequest:   denied,
			CanceledRequest: request(),
		},
	}
}

func TestConform_StoreFetchRejectsBadBehavior(t *testing.T) {
	for _, fault := range []string{"", "receipt", "cancel", "invalid", "unauthorized"} {
		t.Run(fault, func(t *testing.T) {
			s := newStub()
			s.caps = connector.Capabilities{codehost.CapStoreFetch}
			rep, err := codehostconform.Run(context.Background(), storeStub{s, fault}, storeOptions(t))
			if err != nil {
				t.Fatal(err)
			}
			if fault == "" {
				if !rep.OK() {
					t.Fatal(rep.String())
				}
				return
			}
			if rep.OK() {
				t.Fatalf("conformance accepted %s defect", fault)
			}
		})
	}
}

func TestConform_StoreFetchRequiresFixturesAndCapability(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codehost.CapStoreFetch}
	opts := storeOptions(t)
	rep, err := codehostconform.Run(context.Background(), s, opts)
	if err != nil {
		t.Fatal(err)
	}
	requireFailed(t, rep, codehostconform.CheckOptionalDeclared)
	s.caps = nil
	opts.Manifest = stubManifest()
	rep, err = codehostconform.Run(context.Background(), storeStub{s, ""}, opts)
	if err != nil {
		t.Fatal(err)
	}
	requireFailed(t, rep, codehostconform.CheckOptionalDeclared)
	s.caps = connector.Capabilities{codehost.CapStoreFetch}
	opts = storeOptions(t)
	opts.StoreFetch = nil
	if _, err := codehostconform.Run(context.Background(), storeStub{s, ""}, opts); cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("missing fixtures: %v", err)
	}
}

func TestConform_UnimplementedStoreFetchIsNotExercised(t *testing.T) {
	rep := run(t, newStub(), stubManifest(codehost.CapNativeReview))
	if !rep.OK() {
		t.Fatal(rep.String())
	}
	for _, res := range rep.Results {
		if strings.HasPrefix(res.Name, "store_fetch/") {
			t.Fatalf("unimplemented store fetch was exercised: %s", res.Name)
		}
	}
}
