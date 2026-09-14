package transport

import (
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
)

func BootstrapToPB(b *credential.Bootstrap) *connectorpb.BindAuthRequest {
	if b == nil {
		return &connectorpb.BindAuthRequest{}
	}
	names := b.Names()
	keys := make([]*connectorpb.BootstrapEntry, 0, len(names))
	for _, name := range names {
		var val []byte
		if m := b.Get(name); m != nil {
			val = m.Reveal()
		}
		keys = append(keys, &connectorpb.BootstrapEntry{Name: name, Value: val})
	}
	return &connectorpb.BindAuthRequest{Keys: keys}
}

func BootstrapFromPB(pb *connectorpb.BindAuthRequest) *credential.Bootstrap {
	if pb == nil {
		return credential.NewBootstrap(nil)
	}
	keys := make(map[string][]byte, len(pb.GetKeys()))
	for _, e := range pb.GetKeys() {
		if e == nil || e.GetName() == "" {
			continue
		}
		keys[e.GetName()] = e.GetValue()
	}
	return credential.NewBootstrap(keys)
}
