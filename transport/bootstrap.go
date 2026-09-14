package transport

import (
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
)

func BootstrapToPB(b *credential.Bootstrap) *connectorpb.BindAuthRequest {
	return &connectorpb.BindAuthRequest{}
}

func BootstrapFromPB(pb *connectorpb.BindAuthRequest) *credential.Bootstrap {
	return credential.NewBootstrap(nil)
}
