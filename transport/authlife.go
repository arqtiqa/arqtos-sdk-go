package transport

import (
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
)

func AuthSessionToPB(s credential.AuthSession) *connectorpb.AuthSession {
	return &connectorpb.AuthSession{}
}

func AuthSessionFromPB(pb *connectorpb.AuthSession) credential.AuthSession {
	return credential.AuthSession{}
}
