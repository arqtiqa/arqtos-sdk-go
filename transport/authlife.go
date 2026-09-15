package transport

import (
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
)

func AuthSessionToPB(s credential.AuthSession) *connectorpb.AuthSession {
	var exp int64
	if !s.ExpiresAt.IsZero() {
		exp = s.ExpiresAt.Unix()
	}
	return &connectorpb.AuthSession{
		Id:            s.ID,
		Kind:          string(s.Kind),
		ExpiresAtUnix: exp,
		Renewable:     s.Renewable,
	}
}

func AuthSessionFromPB(pb *connectorpb.AuthSession) credential.AuthSession {
	if pb == nil {
		return credential.AuthSession{}
	}
	var exp time.Time
	if pb.GetExpiresAtUnix() != 0 {
		exp = time.Unix(pb.GetExpiresAtUnix(), 0).UTC()
	}
	return credential.AuthSession{
		ID:        pb.GetId(),
		Kind:      credential.HandleKind(pb.GetKind()),
		ExpiresAt: exp,
		Renewable: pb.GetRenewable(),
	}
}
