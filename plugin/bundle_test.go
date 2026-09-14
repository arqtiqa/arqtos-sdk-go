package plugin

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

type bundleMemLoader struct {
	memLoader
}

func (b *bundleMemLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapGrantBundle}
}

func (b *bundleMemLoader) AcquireBundle(_ context.Context, inv credential.Inventory) (credential.Bundle, error) {
	entries := make([]credential.BundleEntry, 0, len(inv.Keys))
	for _, k := range inv.Keys {
		v, ok := b.vals[k.String()]
		if !ok {
			return credential.Bundle{}, cerr.New(cerr.KindNotFound, "AcquireBundle", nil)
		}
		if v == "" {
			entries = append(entries, credential.BundleStoredEmpty(k))
			continue
		}
		res, err := credential.Resolved(credential.NewMaterial([]byte(v)))
		if err != nil {
			return credential.Bundle{}, err
		}
		entries = append(entries, credential.BundleValue(k, res))
	}
	return credential.CompleteBundle(inv, entries, credential.BundleMeta{Generation: "g1", Source: "mem"})
}

var _ credential.BundleAcquirer = (*bundleMemLoader)(nil)

type declaresBundleButCannot struct{ memLoader }

func (d *declaresBundleButCannot) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapGrantBundle}
}

func TestStubShapeMirrorsGrantBundleDeclaration(t *testing.T) {
	t.Run("declared: the stub binds", func(t *testing.T) {
		c := newTestClient(t, &bundleMemLoader{memLoader: memLoader{vals: batchVals()}})
		if _, ok := c.(credential.BundleAcquirer); !ok {
			t.Fatalf("a provider reporting %s must dispense a credential.BundleAcquirer, got %T",
				credential.CapGrantBundle, c)
		}
	})
	t.Run("not declared: the stub does not", func(t *testing.T) {
		c := newTestClient(t, &memLoader{vals: batchVals()})
		if _, ok := c.(credential.BundleAcquirer); ok {
			t.Fatalf("a provider that does not report %s must not dispense a credential.BundleAcquirer",
				credential.CapGrantBundle)
		}
	})
}

func TestAcquireBundleRoundTripOverTheWire(t *testing.T) {
	impl := &bundleMemLoader{memLoader: memLoader{vals: batchVals()}}
	c := newTestClient(t, impl)
	acq, ok := c.(credential.BundleAcquirer)
	if !ok {
		t.Fatalf("dispensed %T, want BundleAcquirer", c)
	}
	keys := []ref.Ref{mustParse(t, knownRef), mustParse(t, otherRef)}
	inv := credential.Inventory{
		Authority:  "sa-placeholder",
		Containers: []string{"v", "<vault>"},
		Keys:       keys,
		Coverage:   credential.CoverageDeclared,
	}
	got, err := acq.AcquireBundle(context.Background(), inv)
	if err != nil {
		t.Fatalf("AcquireBundle: %v", err)
	}
	checked, err := credential.CheckBundle("placeholder-provider", inv, got, nil)
	if err != nil {
		t.Fatalf("CheckBundle: %v", err)
	}
	if !checked.Ready() {
		t.Fatal("round-trip bundle is not ready")
	}
}

func TestResolveDoesNotExportAdjacentGrantedFields(t *testing.T) {
	impl := &bundleMemLoader{memLoader: memLoader{vals: batchVals()}}
	c := newTestClient(t, impl)
	a := mustParse(t, knownRef)
	res, err := c.Resolve(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	mat, err := res.Value()
	if err != nil {
		t.Fatal(err)
	}
	got := string(mat.Reveal())
	if got != knownSecret {
		t.Fatalf("Resolve = %q, want only the requested key", got)
	}
	if strings.Contains(got, otherSecret) {
		t.Fatal("ordinary Resolve exported an adjacent granted field")
	}
}

func TestGrantBundleDeclaredButNotImplementedAnswersUnsupported(t *testing.T) {
	c := newTestClient(t, &declaresBundleButCannot{memLoader{vals: batchVals()}})
	acq, ok := c.(credential.BundleAcquirer)
	if !ok {
		t.Fatalf("dispensed %T, want BundleAcquirer", c)
	}
	_, err := acq.AcquireBundle(context.Background(), credential.Inventory{
		Authority:  "sa-placeholder",
		Containers: []string{"v"},
		Keys:       []ref.Ref{mustParse(t, knownRef)},
		Coverage:   credential.CoverageDeclared,
	})
	if err == nil {
		t.Fatal("declared-but-unimplemented grant_bundle must fail the call")
	}
	if cerr.KindOf(err) != cerr.KindUnsupported {
		t.Fatalf("KindOf = %v, want KindUnsupported", cerr.KindOf(err))
	}
}

type batchBundleMemLoader struct{ bundleMemLoader }

func (b *batchBundleMemLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapBatchResolve, credential.CapGrantBundle}
}

func (b *batchBundleMemLoader) ResolveBatch(ctx context.Context, refs []ref.Ref) ([]credential.BatchResult, error) {
	out := make([]credential.BatchResult, 0, len(refs))
	for _, r := range refs {
		res, err := b.Resolve(ctx, r)
		if err != nil {
			failed, _ := credential.BatchFailed(r, err)
			out = append(out, failed)
			continue
		}
		ok, _ := credential.BatchResolved(r, res)
		out = append(out, ok)
	}
	return out, nil
}

func TestStubShapeMirrorsBatchAndGrantBundleTogether(t *testing.T) {
	c := newTestClient(t, &batchBundleMemLoader{bundleMemLoader: bundleMemLoader{memLoader{vals: batchVals()}}})
	if _, ok := c.(credential.BatchResolver); !ok {
		t.Fatalf("dispensed %T, want BatchResolver", c)
	}
	if _, ok := c.(credential.BundleAcquirer); !ok {
		t.Fatalf("dispensed %T, want BundleAcquirer", c)
	}
}

func TestOldPeerAcquireBundleIsUnimplemented(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	connectorpb.RegisterCredentialLoaderServer(srv, &connectorpb.UnimplementedCredentialLoaderServer{})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_, err = connectorpb.NewCredentialLoaderClient(conn).AcquireBundle(context.Background(), &connectorpb.AcquireBundleRequest{})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unimplemented {
		t.Fatalf("old peer AcquireBundle: %v, want Unimplemented", err)
	}
	if cerr.KindOf(transport.ErrFromStatus(err)) != cerr.KindUnsupported {
		t.Fatalf("KindOf = %v", cerr.KindOf(transport.ErrFromStatus(err)))
	}
}
