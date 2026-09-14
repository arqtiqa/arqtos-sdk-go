package transport

import (
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

func InventoryToPB(inv credential.Inventory) *connectorpb.GrantInventory {
	keys := make([]*connectorpb.Ref, len(inv.Keys))
	for i, k := range inv.Keys {
		keys[i] = RefToPB(k)
	}
	return &connectorpb.GrantInventory{
		Authority:  inv.Authority,
		Containers: append([]string(nil), inv.Containers...),
		Keys:       keys,
		Coverage:   string(inv.Coverage),
	}
}

func InventoryFromPB(pb *connectorpb.GrantInventory) credential.Inventory {
	if pb == nil {
		return credential.Inventory{}
	}
	keys := make([]ref.Ref, 0, len(pb.GetKeys()))
	for _, k := range pb.GetKeys() {
		keys = append(keys, RefFromPB(k))
	}
	return credential.Inventory{
		Authority:  pb.GetAuthority(),
		Containers: append([]string(nil), pb.GetContainers()...),
		Keys:       keys,
		Coverage:   credential.Coverage(pb.GetCoverage()),
	}
}

func BundleToPB(b credential.Bundle) *connectorpb.AcquireBundleResponse {
	meta := b.Meta()
	entries := b.Entries()
	pbEntries := make([]*connectorpb.BundleEntry, 0, len(entries))
	for _, e := range entries {
		pbEntries = append(pbEntries, &connectorpb.BundleEntry{
			Identity: RefToPB(e.Identity()),
			Material: ResolutionToPB(e.Resolution()),
		})
	}
	var acquired int64
	if !meta.AcquiredAt.IsZero() {
		acquired = meta.AcquiredAt.Unix()
	}
	return &connectorpb.AcquireBundleResponse{
		Generation:     meta.Generation,
		Source:         meta.Source,
		AcquiredAtUnix: acquired,
		Coverage:       string(b.Coverage()),
		Entries:        pbEntries,
		Completeness:   connectorpb.BundleCompleteness(b.Completeness()),
	}
}

func BundleFromPB(pb *connectorpb.AcquireBundleResponse) credential.Bundle {
	if pb == nil {
		return credential.Bundle{}
	}
	entries := make([]credential.BundleEntry, 0, len(pb.GetEntries()))
	for _, e := range pb.GetEntries() {
		if e == nil {
			continue
		}
		id := RefFromPB(e.GetIdentity())
		res := ResolutionFromPB(e.GetMaterial())
		entries = append(entries, credential.BundleValue(id, res))
	}
	var acquired time.Time
	if pb.GetAcquiredAtUnix() != 0 {
		acquired = time.Unix(pb.GetAcquiredAtUnix(), 0).UTC()
	}
	return credential.RestoreBundle(
		credential.BundleMeta{Generation: pb.GetGeneration(), Source: pb.GetSource(), AcquiredAt: acquired},
		credential.Coverage(pb.GetCoverage()),
		credential.Completeness(pb.GetCompleteness()),
		entries,
	)
}
