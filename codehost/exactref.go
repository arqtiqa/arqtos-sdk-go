package codehost

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

const (
	CASApplied       CASOutcome = "applied"
	CASMismatch      CASOutcome = "mismatch"
	CASIndeterminate CASOutcome = "indeterminate"
)

// CASOutcome is a definite applied or mismatch result, or an indeterminate
// write that the caller must resolve with [ExactRefTransport.ReadExact].
type CASOutcome string

// ObjectID is a full git object id. Empty means the named ref does not exist.
type ObjectID string

// ExactReadRequest observes one explicit ref in one native repository.
type ExactReadRequest struct {
	Realm    string
	NativeID string
	Ref      string
}

// ExactReadResult is the observed head of the named ref.
type ExactReadResult struct {
	Realm    string
	NativeID string
	Ref      string
	ObjectID ObjectID
}

// CASRequest updates one explicit ref when its current object equals Expected.
//
// SourceDir is the absolute local Git repository or worktree root that already
// contains New and its reachable object closure. It is read-only input: the
// connector must not mutate the source, publish the index, or treat uncommitted
// files as the proposed object. A dirty worktree remains valid because the
// index and worktree are not read. The connector stages the required objects in
// isolated storage without executing source hooks, inheriting credentials,
// trusting arbitrary local Git configuration, or recording alternates back to
// the source. Path-syntax validation does not prove those properties.
// Credentials stay constructor state and are absent from this request.
type CASRequest struct {
	Realm     string
	NativeID  string
	Ref       string
	Expected  ObjectID
	New       ObjectID
	SourceDir string
}

// CASReceipt binds repository identity, ref, object ids and outcome.
// It is not policy approval.
type CASReceipt struct {
	Realm    string
	NativeID string
	Ref      string
	Expected ObjectID
	Observed ObjectID
	New      ObjectID
	Outcome  CASOutcome
}

// ExactRefTransport is the optional all-or-nothing read-and-CAS surface
// behind [CapExactRef]. Write support without the matching exact read cannot
// recover an indeterminate outcome.
type ExactRefTransport interface {
	ReadExact(ctx context.Context, req ExactReadRequest) (ExactReadResult, error)
	CompareAndSwap(ctx context.Context, req CASRequest) (CASReceipt, error)
}

func (r ExactReadRequest) Validate() error {
	return validateRepoRef("ExactReadRequest.Validate", r.Realm, r.NativeID, r.Ref)
}

func (r CASRequest) Validate() error {
	if err := validateRepoRef("CASRequest.Validate", r.Realm, r.NativeID, r.Ref); err != nil {
		return err
	}
	if r.Expected != "" && !validObjectID(r.Expected) {
		return cerr.New(cerr.KindInvalid, "CASRequest.Validate", fmt.Errorf("expected object id is not a full object id"))
	}
	if !validObjectID(r.New) {
		return cerr.New(cerr.KindInvalid, "CASRequest.Validate", fmt.Errorf("new object id is required"))
	}
	if !validSourceDir(r.SourceDir) {
		return cerr.New(cerr.KindInvalid, "CASRequest.Validate", fmt.Errorf("source must be an explicit absolute local git repository"))
	}
	return nil
}

func validateRepoRef(op, realm, nativeID, ref string) error {
	if realm == "" || nativeID == "" || ref == "" {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("realm, native repository id and ref are required"))
	}
	if !validRealm(realm) {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("unsafe realm"))
	}
	if !validNativeID(nativeID) {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("native repository id must not be a path"))
	}
	if !validRef(ref) {
		return cerr.New(cerr.KindInvalid, op, fmt.Errorf("unsafe ref"))
	}
	return nil
}

func validSourceDir(p string) bool {
	if p == "" || p == "/" {
		return false
	}
	if strings.Contains(p, "://") {
		return false
	}
	if containsUnsafeRunes(p) {
		return false
	}
	if !filepath.IsAbs(p) {
		return false
	}
	return filepath.Clean(p) == p
}

func validRealm(realm string) bool {
	if containsUnsafeRunes(realm) {
		return false
	}
	if !strings.Contains(realm, "://") {
		return true
	}
	u, err := url.Parse(realm)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Path == "" || u.Path == "/"
}

func validNativeID(id string) bool {
	if id == "" || strings.Contains(id, "/") || id[0] == '-' {
		return false
	}
	return !containsUnsafeRunes(id)
}

func validRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/") {
		return false
	}
	if strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") {
		return false
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") {
		return false
	}
	if containsUnsafeRunes(ref) {
		return false
	}
	if strings.ContainsAny(ref, "~^:?*[\\") {
		return false
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func containsUnsafeRunes(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func validObjectID(id ObjectID) bool {
	n := len(id)
	if n != 40 && n != 64 {
		return false
	}
	for i := 0; i < n; i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
