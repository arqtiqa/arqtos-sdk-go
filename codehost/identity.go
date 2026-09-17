package codehost

import (
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// NativeIdentity is the host-scoped repository identity used for full UID
// issuance (dcn-arq-00031 rule 4). ID is opaque; Realm is the provider instance.
type NativeIdentity struct {
	ID    string
	Realm string
}

// NativeIdentity reports the verified native repository identity.
// wantRealm, when set, must match Repo.Realm.
func (r Repo) NativeIdentity(wantRealm string) (NativeIdentity, error) {
	if r.NativeID == "" {
		if r.Realm != "" {
			return NativeIdentity{}, cerr.New(cerr.KindUnsupported, "NativeIdentity",
				fmt.Errorf("provider instance has no native repository id"))
		}
		return NativeIdentity{}, cerr.New(cerr.KindContractViolation, "NativeIdentity",
			fmt.Errorf("native repository id is missing"))
	}
	if r.Realm == "" {
		return NativeIdentity{}, cerr.New(cerr.KindContractViolation, "NativeIdentity",
			fmt.Errorf("provider realm is missing"))
	}
	if strings.Contains(r.NativeID, "/") || r.NativeID == r.FullName {
		return NativeIdentity{}, cerr.New(cerr.KindInvalid, "NativeIdentity",
			fmt.Errorf("native repository id must not be a path"))
	}
	if wantRealm != "" && r.Realm != wantRealm {
		return NativeIdentity{}, cerr.New(cerr.KindInvalid, "NativeIdentity",
			fmt.Errorf("provider realm mismatch"))
	}
	return NativeIdentity{ID: r.NativeID, Realm: r.Realm}, nil
}
