// Package transport marshals the SDK's semantic contract types (ref.Ref,
// credential.Resolution, credential.Lease, cerr.Error) to and from the
// generated connectorpb wire types and gRPC status, so a Track-B provider and
// host can exchange them over the CredentialLoader gRPC service.
//
// Material has no ToPB helper of its own: material crosses the wire only
// inside a Resolution, whose presence rules the wire has to preserve
// ([ResolutionToPB]), and the receiving side re-wraps the bytes through
// credential.NewMaterial so redaction and Zero() hold host-side.
//
// The single most important thing in this package is [ResolutionFromPB]: it
// is where a foreign provider's bytes become a host-side credential, and the
// only place that decides whether "no bytes" means a deliberately-empty
// secret or nothing at all. Read its comment before changing it.
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
// The three in-process states cross as three distinct encodings:
//
//	unresolved           -> nil message (no Material at all)
//	deliberately empty   -> present message, no bytes, EmptyByAssertion set
//	a value              -> present message carrying the bytes
//
// The assertion flag is what makes the distinction survive a foreign sender.
// Presence alone cannot: proto3 does not put a zero-length bytes field on the
// wire, so a conformant ResolvedEmpty and a provider that resolved nothing
// and sent a default-constructed Material would be byte-identical. Writing
// the flag here is the sending half of that; ResolutionFromPB is the half
// that matters.
func ResolutionToPB(r credential.Resolution) *connectorpb.Material {
	mat, err := r.Value()
	if err != nil {
		// Nothing was resolved. Sending an empty Material here is exactly the
		// conflation the contract forbids, so nothing is sent.
		return nil
	}
	b := mat.Reveal()
	if len(b) == 0 {
		// Readable and zero-length: the sender asserted a deliberately-empty
		// secret. Say so explicitly, because the bytes cannot.
		return &connectorpb.Material{EmptyByAssertion: true}
	}
	return &connectorpb.Material{Value: b}
}

// ResolutionFromPB converts a wire Material back to a credential.Resolution.
//
// It reads FOUR cases, and the fourth is the point:
//
//	nil message                        -> unresolved (unreadable)
//	bytes present                      -> a value
//	no bytes, EmptyByAssertion set     -> a deliberately-empty value
//	no bytes, NO assertion             -> unresolved (unreadable)
//
// That last case is what a confused or lazy foreign provider sends when it
// resolved nothing: an empty, default-constructed Material. Reading it as a
// deliberately-empty credential — which an earlier revision of this function
// did — reopens, at the one boundary where the host cannot inspect the
// sender's code, precisely the (empty value, no error) bug the contract
// exists to eliminate. Emptiness has to be ASSERTED, never inferred, or the
// dangerous meaning is the one an author reaches by accident.
//
// The host does not merely refuse the value: credential.CheckResolution turns
// the unresolved reading into a named fault, so the operator learns WHICH
// connector did it rather than watching a credential be quietly empty.
func ResolutionFromPB(pb *connectorpb.Material) credential.Resolution {
	if pb == nil {
		return credential.Resolution{}
	}
	if len(pb.GetValue()) == 0 {
		if !pb.GetEmptyByAssertion() {
			return credential.Resolution{}
		}
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

// FailureToPB encodes a PER-REFERENCE failure for a batch response, where the
// RPC itself succeeded and one reference did not.
//
// It carries the same classification a whole-call failure would: the error is
// mapped through ErrToStatus, and the resulting gRPC code travels as a
// number. A host maps it back with FailureFromPB and acts on the Kind, never
// on the message text. A nil error yields a nil message.
func FailureToPB(err error) *connectorpb.Failure {
	if err == nil {
		return nil
	}
	st := status.Convert(ErrToStatus(err))
	return &connectorpb.Failure{Code: int32(st.Code()), Message: st.Message()}
}

// FailureFromPB reconstructs a per-reference failure as a *cerr.Error, the
// reverse of FailureToPB. A nil message — and a message whose code is OK,
// which is not a failure at all — yields a nil error, so a result that
// carries no real failure reads as carrying none, and credential.CheckBatch
// then reports it as the empty outcome it is.
func FailureFromPB(pb *connectorpb.Failure) error {
	if pb == nil {
		return nil
	}
	return ErrFromStatus(status.Error(codes.Code(pb.GetCode()), pb.GetMessage()))
}

// BatchResultToPB converts one credential.BatchResult to the wire.
//
// Exactly one of material/failure is set, mirroring the type: a result that
// carries a failure sends the failure, and any other result sends its
// material under ResolutionToPB's presence rules — including the assertion
// flag for a deliberately-empty value.
func BatchResultToPB(b credential.BatchResult) *connectorpb.ResolveBatchResult {
	out := &connectorpb.ResolveBatchResult{Ref: RefToPB(b.Ref())}
	if err := b.Err(); err != nil {
		out.Failure = FailureToPB(err)
		return out
	}
	out.Material = ResolutionToPB(b.Resolution())
	return out
}

// BatchResultFromPB converts one wire result back to a
// credential.BatchResult.
//
// A result that carries neither a failure nor readable material comes back as
// a BatchResult with no outcome — never as an empty credential. That is the
// same refusal ResolutionFromPB makes, one level down: credential.CheckBatch
// then faults the batch and names the connector and the position.
func BatchResultFromPB(pb *connectorpb.ResolveBatchResult) credential.BatchResult {
	if pb == nil {
		return credential.BatchResult{}
	}
	r := RefFromPB(pb.GetRef())
	if err := FailureFromPB(pb.GetFailure()); err != nil {
		// Cannot fault: err is non-nil.
		res, _ := credential.BatchFailed(r, err)
		return res
	}
	// A blank result yields a BatchResult carrying no outcome, which is what
	// the discarded error says. CheckBatch is where that is reported, with
	// the position and the connector's name attached.
	res, _ := credential.BatchResolved(r, ResolutionFromPB(pb.GetMaterial()))
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
