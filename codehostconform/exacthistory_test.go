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

type historyStub struct {
	*stub
	fault string
}

// This broken connector validates destination state but never validates an
// empty Expected. A populated destination must not mask that missing check.
type destinationOnlyHistory struct {
	historyStub
	populated map[string]bool
}

func (s *destinationOnlyHistory) AcquireExact(ctx context.Context, r codehost.ExactHistoryRequest) (codehost.ExactHistoryResult, error) {
	if s.populated[r.DestinationDir] {
		return codehost.ExactHistoryResult{}, cerr.New(cerr.KindInvalid, "AcquireExact", errors.New("nonempty destination"))
	}
	if r.Expected == "" {
		return codehost.ExactHistoryResult{}, nil
	}
	x, err := s.historyStub.AcquireExact(ctx, r)
	if err == nil {
		s.populated[r.DestinationDir] = true
	}
	return x, err
}

func TestConform_InvalidOIDProbeIsNotMaskedByPopulatedDestination(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codehost.CapExactHistory}
	c := &destinationOnlyHistory{historyStub: historyStub{s, ""}, populated: map[string]bool{}}
	rep, err := codehostconform.Run(context.Background(), c, historyOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	requireFailed(t, rep, "exact_history/invalid-is-refused")
}

func (s historyStub) AcquireExact(ctx context.Context, r codehost.ExactHistoryRequest) (codehost.ExactHistoryResult, error) {
	var zero codehost.ExactHistoryResult
	if ctx.Err() != nil {
		if s.fault == "cancel" {
			return zero, ctx.Err()
		}
		return zero, cerr.New(cerr.KindTimeout, "AcquireExact", ctx.Err())
	}
	if err := r.Validate(); err != nil {
		if s.fault == "invalid" {
			return zero, nil
		}
		return zero, err
	}
	if strings.HasPrefix(string(r.Expected), "b") {
		if s.fault == "race" {
			return zero, cerr.New(cerr.KindInvalid, "AcquireExact", errors.New("unrelated bad input"))
		}
		return zero, cerr.New(cerr.KindInvalid, "AcquireExact", codehost.ErrRefChanged)
	}
	x := codehost.ExactHistoryResult{Realm: r.Realm, NativeID: r.NativeID, Ref: r.Ref, ObjectID: r.Expected, DestinationDir: r.DestinationDir, Complete: true}
	if s.fault == "receipt" {
		x.ObjectID = codehost.ObjectID(strings.Repeat("c", 40))
	}
	if s.fault == "closure" {
		x.Complete = false
	}
	return x, nil
}

func historyOptions(t *testing.T) codehostconform.Options {
	t.Helper()
	request := func() codehost.ExactHistoryRequest {
		return codehost.ExactHistoryRequest{Realm: "host-a", NativeID: "1", Ref: "refs/heads/main", Expected: codehost.ObjectID(strings.Repeat("a", 40)), DestinationDir: t.TempDir()}
	}
	stale := request()
	stale.Expected = codehost.ObjectID(strings.Repeat("b", 40))
	return codehostconform.Options{Manifest: stubManifest(codehost.CapExactHistory), ListableOwner: fixtureListable, UnlistableOwner: fixtureUnlistable, MissingOwner: fixtureMissingOwner, ExactHistory: &codehostconform.ExactHistoryFixtures{Request: request(), StaleRequest: stale, CanceledRequest: request()}}
}

func TestConform_ExactHistoryRejectsBadBehavior(t *testing.T) {
	for _, fault := range []string{"", "receipt", "closure", "cancel", "race", "invalid"} {
		t.Run(fault, func(t *testing.T) {
			s := newStub()
			s.caps = connector.Capabilities{codehost.CapExactHistory}
			rep, err := codehostconform.Run(context.Background(), historyStub{s, fault}, historyOptions(t))
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

func TestConform_ExactHistoryRequiresFixturesAndCapability(t *testing.T) {
	s := newStub()
	s.caps = connector.Capabilities{codehost.CapExactHistory}
	opts := historyOptions(t)
	rep, err := codehostconform.Run(context.Background(), s, opts)
	if err != nil {
		t.Fatal(err)
	}
	requireFailed(t, rep, codehostconform.CheckOptionalDeclared)
	s.caps = nil
	opts.Manifest = stubManifest()
	rep, err = codehostconform.Run(context.Background(), historyStub{s, ""}, opts)
	if err != nil {
		t.Fatal(err)
	}
	requireFailed(t, rep, codehostconform.CheckOptionalDeclared)
	s.caps = connector.Capabilities{codehost.CapExactHistory}
	opts = historyOptions(t)
	opts.ExactHistory = nil
	if _, err := codehostconform.Run(context.Background(), historyStub{s, ""}, opts); cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("missing fixtures: %v", err)
	}
	opts = historyOptions(t)
	opts.ExactHistory.StaleRequest.DestinationDir = opts.ExactHistory.Request.DestinationDir
	if _, err := codehostconform.Run(context.Background(), historyStub{s, ""}, opts); cerr.KindOf(err) != cerr.KindInvalid {
		t.Fatalf("shared destinations: %v", err)
	}
}
