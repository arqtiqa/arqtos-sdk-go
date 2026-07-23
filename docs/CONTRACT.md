# The `CredentialLoader` contract

This document describes the method-by-method semantics of the
[`credential.CredentialLoader`](../credential/credential.go) connector class, the
capability set it is gated by, and the [`cerr`](../cerr/cerr.go) error taxonomy
every contract method returns errors in.

`CredentialLoader` is implemented by both **native** (in-process, compiled into
the host) and **out-of-process** connectors. Nothing in this document is
specific to either runtime shape — see [Versioning](#versioning) for where the
out-of-process wire protocol is defined.

## The base contract

Every connector, regardless of class, implements
[`connector.Connector`](../connector/connector.go):

| Method | Semantics |
|---|---|
| `Implements() Class` | Returns the connector class this instance implements — `connector.ClassCredentialLoader` for a `CredentialLoader`. Used by the host to route by class without a type assertion. |
| `Capabilities() Capabilities` | Returns the set of optional behaviors this instance supports (see [Capabilities](#capabilities) below). The host MUST check `Capabilities().Has(...)` before calling an optional method, and a connector MUST return `cerr.KindUnsupported` from any method it advertises as unsupported. |
| `Health(ctx) (Health, error)` | Reports current reachability of the backing store: `Healthy`, `Degraded`, or `Unavailable`, with a free-text `Detail`. Must respect `ctx` cancellation/deadline. Used for host-side circuit-breaking and status surfaces — it is not itself an auth check. |
| `Close() error` | Releases any held resources (connections, background goroutines, cached material). Must be safe to call once at end of connector lifetime; a `CredentialLoader` MUST NOT retain resolved `Material` past `Close()` — see [`docs/SECURITY.md`](SECURITY.md). |

## `CredentialLoader` methods

`CredentialLoader` embeds `connector.Connector` and adds:

| Method | Semantics |
|---|---|
| `Resolve(ctx, r ref.Ref) (*Material, error)` | Resolves a single `op://vault/item/field` reference to its current secret material. This is the base operation — a `CredentialLoader` MUST support `Resolve` (gated by `CapRead`). Returns `cerr.KindNotFound` if the ref does not exist, `cerr.KindUnauthorized` if the caller lacks access, `cerr.KindInvalid` for a malformed ref, `cerr.KindUnavailable`/`cerr.KindTimeout` for transient backing-store failures. |
| `List(ctx, scope string) ([]ref.Ref, error)` | Lists the `ref.Ref`s visible under `scope` (a connector-defined scope string, e.g. a vault name). Returns refs only — never resolves material for the listed entries. A connector that cannot enumerate (e.g. a store with no listing API) returns `cerr.KindUnsupported`. |
| `Lease(ctx, r ref.Ref) (*Material, Lease, error)` | Like `Resolve`, but for a backing store that issues **dynamic**, time-bounded secrets (e.g. a database credential broker): returns material plus a `Lease` describing its `TTL`/`ExpiresAt`/`Renewable`. Gated by `CapLease`; a connector without dynamic-secret support returns `cerr.KindUnsupported`. |
| `Renew(ctx, l Lease) (Lease, error)` | Extends a `Lease` obtained from `Lease(...)`, returning the renewed `Lease` (new `ExpiresAt`). Returns `cerr.KindInvalid` for an unknown/expired lease ID, `cerr.KindUnsupported` if `l.Renewable` is false or the connector lacks `CapLease`. |
| `Revoke(ctx, l Lease) error` | Proactively invalidates a `Lease` before its natural expiry (e.g. on session end — see [`docs/SECURITY.md`](SECURITY.md), "dies-with-session"). Idempotent: revoking an already-revoked or expired lease is not an error. |

### `Material` and `Lease`

- `credential.Material` holds resolved secret bytes. `String()`/`GoString()`
  always return a fixed redacted placeholder — the raw bytes are reachable
  only through the explicit `Reveal()` call. `Zero()` wipes the backing bytes
  in place; callers MUST call it once material is no longer needed. See
  [`docs/SECURITY.md`](SECURITY.md) for the full handling rule.
- `credential.Lease{ID, TTL, ExpiresAt, Renewable}` describes a dynamic
  secret's validity window. `Lease.Expired(now)` is a pure function of an
  injected `now`, so lease-expiry logic is deterministically testable without
  wall-clock sleeps.

## Capabilities

A `CredentialLoader`'s `Capabilities()` return value is drawn from the
capability constants declared in the `credential` package:

| Capability | Meaning |
|---|---|
| `CapRead` | Supports `Resolve`/`List` of static secret material. The baseline capability — expected on every `CredentialLoader`. |
| `CapLease` | Supports `Lease`/`Renew`/`Revoke` for dynamic, time-bounded secrets. |
| `CapRotate` | Supports triggering rotation of the underlying secret at the backing store (rotation itself is out of scope for this contract version; the capability marks that the connector's backing store can be asked to rotate). |
| `CapOIDC` | The connector authenticates to its backing store via OIDC federation (no long-lived credential held by the connector itself). |
| `CapAppRole` | The connector authenticates to its backing store via an AppRole-style (role-id/secret-id) mechanism. |

`CapOIDC` and `CapAppRole` describe how the connector itself authenticates
outward, not a behavior it exposes inward — hosts use them to reason about the
connector's own credential posture, e.g. for audit and rotation policy.

A host MUST call `Capabilities().Has(cap)` before invoking a
capability-gated method, and a connector MUST return
`cerr.New(cerr.KindUnsupported, op, nil)` from any method whose capability it
does not advertise, rather than silently no-op'ing.

## The `cerr.Kind` taxonomy

Every error a contract method returns is a `*cerr.Error{Kind, Op, Err}`.
Callers classify with `cerr.KindOf(err)` / `cerr.Retryable(err)` — never by
matching on the error string.

| `Kind` | Meaning | Retryable |
|---|---|---|
| `KindUnknown` | Default/unclassified — e.g. a plain error that never passed through `cerr.New`. | no |
| `KindNotFound` | The referenced secret, scope, or lease does not exist. | no |
| `KindUnauthorized` | The caller/connector identity lacks access to the resource. | no |
| `KindUnavailable` | The backing store is transiently unreachable (network, outage). | yes |
| `KindUnsupported` | The operation is not implemented by this connector/capability set. | no |
| `KindInvalid` | The input (a malformed `ref.Ref`, an unknown lease ID, ...) is invalid. | no |
| `KindTimeout` | The operation did not complete within its deadline. | yes |

`cerr.Retryable(err)` is `true` exactly for `KindUnavailable` and
`KindTimeout` — the two kinds where retrying the same call, generally after a
backoff, may succeed without any change in caller behavior.

## Versioning

This module defines the **Go semantic contract** only — interfaces, value
types, and the error taxonomy. It intentionally does not define:

- a wire protocol (`.proto`/gRPC) for out-of-process connectors,
- a connector registry or manifest format,
- host/connector version negotiation (`min_host_version`).

Those land in a separate, later contract (Story #808). A connector built
against this module today is forward-compatible with that protocol: the Go
interfaces here are what the wire protocol will marshal to/from, not something
it replaces.
