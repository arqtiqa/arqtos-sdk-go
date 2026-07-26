// Package transport marshals the SDK's semantic contract types (ref.Ref,
// credential.Resolution, credential.Lease, cerr.Error) to and from the
// generated connectorpb wire types and gRPC status, so a Track-B provider and
// host can exchange them over the CredentialLoader gRPC service.
//
// Material has no ToPB helper of its own: material crosses the wire only
// inside a Resolution, whose presence rules the wire has to preserve
// ([ResolutionToPB]), and the receiving side re-wraps the bytes through
// credential.NewMaterial so redaction and Zero() hold host-side.
package transport

import (
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connectorpb"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// RefToPB converts a ref.Ref to its wire representation.
func RefToPB(r ref.Ref) *connectorpb.Ref {
	return &connectorpb.Ref{Vault: r.Vault, Item: r.Item, Field: r.Field}
}

// RefFromPB converts a wire Ref back to ref.Ref. A nil pb yields the zero Ref.
func RefFromPB(pb *connectorpb.Ref) ref.Ref {
	if pb == nil {
		return ref.Ref{}
	}
	return ref.Ref{Vault: pb.GetVault(), Item: pb.GetItem(), Field: pb.GetField()}
}

// LeaseToPB converts a credential.Lease to its wire representation. TTL
// crosses as whole seconds; ExpiresAt crosses as a unix-seconds timestamp.
func LeaseToPB(l credential.Lease) *connectorpb.Lease {
	return &connectorpb.Lease{
		Id:            l.ID,
		TtlSeconds:    int64(l.TTL / time.Second),
		ExpiresAtUnix: l.ExpiresAt.Unix(),
		Renewable:     l.Renewable,
	}
}

// LeaseFromPB converts a wire Lease back to credential.Lease. A nil pb
// yields the zero Lease.
func LeaseFromPB(pb *connectorpb.Lease) credential.Lease {
	if pb == nil {
		return credential.Lease{}
	}
	return credential.Lease{
		ID:        pb.GetId(),
		TTL:       time.Duration(pb.GetTtlSeconds()) * time.Second,
		ExpiresAt: time.Unix(pb.GetExpiresAtUnix(), 0).UTC(),
		Renewable: pb.GetRenewable(),
	}
}

// ResolutionToPB converts a credential.Resolution to the wire Material.
//
// Presence carries the distinction the contract makes in-process: a
// resolution that carries no value becomes a NIL message — no Material at all
// — while a deliberately-empty value becomes a present message with no bytes.
// A receiver therefore cannot mistake one for the other, and cannot read
// "nothing was resolved" as an empty credential.
func ResolutionToPB(r credential.Resolution) *connectorpb.Material {
	mat, err := r.Value()
	if err != nil {
		// Nothing was resolved. Sending an empty Material here is exactly the
		// conflation REQ-ARQ-P-17 forbids, so nothing is sent.
		return nil
	}
	return &connectorpb.Material{Value: mat.Reveal()}
}

// ResolutionFromPB converts a wire Material back to a credential.Resolution,
// reading presence the same way ResolutionToPB writes it: a nil message is
// unresolved (and stays unreadable), a present message with no bytes is a
// deliberately-empty value.
func ResolutionFromPB(pb *connectorpb.Material) credential.Resolution {
	if pb == nil {
		return credential.Resolution{}
	}
	if len(pb.GetValue()) == 0 {
		return credential.ResolvedEmpty()
	}
	res, err := credential.Resolved(credential.NewMaterial(pb.GetValue()))
	if err != nil {
		// Unreachable: the value is non-empty. Returning the zero Resolution
		// keeps the failure unreadable rather than inventing a value.
		return credential.Resolution{}
	}
	return res
}

// kindToCode is the cerr.Kind -> gRPC codes.Code table. Every known Kind
// maps to its own distinct code; a Kind with no entry (including
// cerr.KindUnknown) falls back to codes.Unknown in ErrToStatus.
var kindToCode = map[cerr.Kind]codes.Code{
	cerr.KindNotFound:     codes.NotFound,
	cerr.KindUnauthorized: codes.PermissionDenied,
	cerr.KindUnavailable:  codes.Unavailable,
	cerr.KindUnsupported:  codes.Unimplemented,
	cerr.KindInvalid:      codes.InvalidArgument,
	cerr.KindTimeout:      codes.DeadlineExceeded,
	// A quota refusal is the one failure a host's breaker acts on, so it
	// needs its own code: collapsed into Unknown it would cross the wire as
	// something the breaker ignores.
	cerr.KindRateLimited: codes.ResourceExhausted,
	// The connector is broken, not the backend — Internal rather than a
	// caller-facing code, and distinct so a host can tell a faulty connector
	// from a failed call after the error has crossed a process boundary.
	cerr.KindContractViolation: codes.Internal,
}

// codeToKind is the reverse of kindToCode, built once at init from the same
// table so the two directions can never drift out of sync.
var codeToKind = func() map[codes.Code]cerr.Kind {
	m := make(map[codes.Code]cerr.Kind, len(kindToCode))
	for k, c := range kindToCode {
		m[c] = k
	}
	return m
}()

// ErrToStatus maps err to a gRPC status error keyed on cerr.KindOf(err).
// An error with no known Kind (including a plain, non-cerr error, or
// cerr.KindUnknown) maps to codes.Unknown. The message is preserved
// verbatim; nil in yields nil out.
func ErrToStatus(err error) error {
	if err == nil {
		return nil
	}
	code, ok := kindToCode[cerr.KindOf(err)]
	if !ok {
		code = codes.Unknown
	}
	return status.Error(code, err.Error())
}

// ErrFromStatus reconstructs a *cerr.Error from a gRPC status error,
// mapping the status code back to a cerr.Kind via codeToKind. An err that
// is not a gRPC status error is wrapped as cerr.KindUnknown; nil in yields
// nil out.
func ErrFromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok || st == nil {
		return cerr.New(cerr.KindUnknown, "", err)
	}
	kind, ok := codeToKind[st.Code()]
	if !ok {
		kind = cerr.KindUnknown
	}
	return cerr.New(kind, "", errors.New(st.Message()))
}
