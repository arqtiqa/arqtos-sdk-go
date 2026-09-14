package transport_test

import (
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

func TestBundleRoundTripDeclaredInventory(t *testing.T) {
	a := ref.Ref{Vault: "v", Item: "i", Field: "f"}
	b := ref.Ref{Vault: "v", Item: "other", Field: "f"}
	inv := credential.Inventory{
		Authority:  "sa-placeholder",
		Containers: []string{"v"},
		Keys:       []ref.Ref{a, b},
		Coverage:   credential.CoverageDeclared,
	}
	va, err := credential.Resolved(credential.NewMaterial([]byte("alpha-secret")))
	if err != nil {
		t.Fatal(err)
	}
	gotInv := transport.InventoryFromPB(transport.InventoryToPB(inv))
	if gotInv.Authority != inv.Authority || gotInv.Coverage != inv.Coverage || len(gotInv.Keys) != 2 {
		t.Fatalf("inventory round-trip = %+v", gotInv)
	}
	bundle, err := credential.CompleteBundle(inv, []credential.BundleEntry{
		credential.BundleValue(a, va),
		credential.BundleStoredEmpty(b),
	}, credential.BundleMeta{Generation: "g1", Source: "fixture", AcquiredAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	got := transport.BundleFromPB(transport.BundleToPB(bundle))
	if !got.Ready() {
		t.Fatal("complete bundle lost completeness across the wire")
	}
	res, ok := got.Lookup(a)
	if !ok {
		t.Fatal("lost enrolled key")
	}
	mat, err := res.Value()
	if err != nil || string(mat.Reveal()) != "alpha-secret" {
		t.Fatalf("value = %v %v", mat, err)
	}
	empty, ok := got.Lookup(b)
	if !ok {
		t.Fatal("stored empty became missing")
	}
	emat, err := empty.Value()
	if err != nil || len(emat.Reveal()) != 0 {
		t.Fatalf("empty = %v %v", emat, err)
	}
}

func TestBundleFromPBUnspecifiedCompletenessIsNotReady(t *testing.T) {
	got := transport.BundleFromPB(&connectorpb.AcquireBundleResponse{
		Entries: []*connectorpb.BundleEntry{{
			Identity: &connectorpb.Ref{Vault: "v", Item: "i", Field: "f"},
			Material: &connectorpb.Material{Value: []byte("x")},
		}},
	})
	if got.Ready() {
		t.Fatal("unspecified completeness must not be ready")
	}
}
