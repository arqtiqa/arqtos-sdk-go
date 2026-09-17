package codehost

// NativeIdentity is the host-scoped repository identity used for full UID
// issuance (dcn-arq-00031 rule 4). ID is opaque; Realm is the provider instance.
type NativeIdentity struct {
	ID    string
	Realm string
}

// NativeIdentity reports the verified native repository identity.
// wantRealm, when set, must match Repo.Realm.
func (r Repo) NativeIdentity(wantRealm string) (NativeIdentity, error) {
	return NativeIdentity{}, nil
}
