// Package transport marshals the SDK's semantic contract types (ref.Ref,
// credential.Lease, cerr.Error) to and from the generated connectorpb wire
// types and gRPC status, so a Track-B provider and host can exchange them
// over the CredentialLoader gRPC service. Material deliberately has no
// ToPB helper here: it crosses the wire as raw bytes and the client wraps
// them via credential.NewMaterial so redaction/Zero() hold host-side.
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
