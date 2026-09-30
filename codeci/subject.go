package codeci

import (
	"context"
	"strconv"
	"strings"
)

// A SubjectKind is how the credential that authenticated the subject was
// provisioned. Zero value means nothing was said and is refused — see
// [SubjectKind.Specified].
//
// PAT is the human personal-access-token profile. App and Service are
// provisioning identities; a host MUST NOT infer human membership from them.
type SubjectKind int

const (
	// SubjectKindUnspecified is the zero value: no kind was named.
	SubjectKindUnspecified SubjectKind = iota
	// SubjectKindPAT is a human personal access token.
	SubjectKindPAT
	// SubjectKindApp is an application installation (GitHub App).
	SubjectKindApp
	// SubjectKindService is a service or bot account.
	SubjectKindService
)

var subjectKindNames = map[SubjectKind]string{
	SubjectKindUnspecified: "unspecified",
	SubjectKindPAT:         "pat",
	SubjectKindApp:         "app",
	SubjectKindService:     "service",
}

// Valid reports whether k names a kind in the closed vocabulary.
func (k SubjectKind) Valid() bool {
	_, ok := subjectKindNames[k]
	return ok
}

// Specified reports whether k is a usable kind: in the vocabulary and not
// [SubjectKindUnspecified].
func (k SubjectKind) Specified() bool { return k.Valid() && k != SubjectKindUnspecified }

// String renders k's stable name.
func (k SubjectKind) String() string {
	if name, ok := subjectKindNames[k]; ok {
		return name
	}
	return "invalid_subject_kind(" + strconv.Itoa(int(k)) + ")"
}

// Subject is the provider-native authenticated subject a credential binds.
// NativeID is opaque and scoped to Authority; Login is display and is not
// identity.
type Subject struct {
	// NativeID is the provider's opaque subject identifier. It MUST NOT be a
	// login, email, URL, or repository path.
	NativeID string
	// Authority is the provider realm (for example "github.com"). It MUST NOT
	// be a URL or a repository locator.
	Authority string
	// Kind is how the credential was provisioned. It MUST be Specified.
	Kind SubjectKind
	// Login is the current display name. A rename MUST NOT change NativeID.
	Login string
}

// Coherent reports whether s carries a scoped native identity rather than a
// locator, an email, or a forgotten field.
func (s Subject) Coherent() bool {
	if s.NativeID == "" || s.Authority == "" || !s.Kind.Specified() {
		return false
	}
	if locator(s.NativeID) || emailish(s.NativeID) {
		return false
	}
	if locator(s.Authority) {
		return false
	}
	return true
}

// SameIdentity reports whether s and other name the same native subject,
// ignoring Login. Both MUST be Coherent.
func (s Subject) SameIdentity(other Subject) bool {
	return s.Coherent() && other.Coherent() && s.NativeID == other.NativeID && s.Authority == other.Authority
}

func locator(v string) bool {
	return strings.Contains(v, "/") || strings.Contains(v, "://")
}

func emailish(v string) bool { return strings.Contains(v, "@") }

// SubjectReporter is the optional contract operation behind
// [CapAuthenticatedSubject]: report the provider-native subject this
// connector's credential authenticates as.
//
// A connector that can do this does two things, and must do both: implement
// this interface, and declare [CapAuthenticatedSubject] in its manifest and
// from Capabilities().
type SubjectReporter interface {
	// AuthenticatedSubject reports the native subject this connector's
	// credential binds.
	//
	// It MUST NOT return a [Subject] that is not [Subject.Coherent]. A
	// credential the code host rejected is cerr.KindUnauthorized; a subject
	// that could not be read is cerr.KindUnavailable; a profile this
	// connector does not yet support (GitHub App, GitHub service, GitLab) is
	// cerr.KindUnsupported.
	AuthenticatedSubject(ctx context.Context) (Subject, error)
}

// CheckSubject is the host-side guard on [SubjectReporter.AuthenticatedSubject].
// An incoherent subject is [FaultIncoherentSubject]; a classified connector
// failure passes through unchanged.
func CheckSubject(connectorName string, s Subject, err error) (Subject, error) {
	const op = "AuthenticatedSubject"
	if err != nil {
		return Subject{}, attribute(connectorName, op, err)
	}
	if !s.Coherent() {
		return Subject{}, &FaultError{
			Connector: connectorName,
			Op:        op,
			Fault:     FaultIncoherentSubject,
			Detail:    "reported a subject missing native id, authority, or kind, or carrying a locator or email as identity",
		}
	}
	return s, nil
}
