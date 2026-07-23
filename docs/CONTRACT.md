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

This module defines the **Go semantic contract** above: interfaces, value
types, and the error taxonomy. Everything on this page applies identically
whether a `CredentialLoader` is compiled into the host (native) or runs
out-of-process (Track-B) — the wire layer below only ever marshals to/from
the same `credential.CredentialLoader`, `ref.Ref`, `credential.Lease`, and
`cerr.Error` types.

### Track-B: the out-of-process wire contract

A Track-B connector is a separate binary — a **provider** — that a host
launches as a subprocess and talks to over gRPC via
[`hashicorp/go-plugin`](https://github.com/hashicorp/go-plugin). The wire
layer composes go-plugin rather than reinventing process/handshake/transport
management; this module adds only the pieces specific to the
`CredentialLoader` class:

| Layer | Package | What it is |
|---|---|---|
| Contract | [`proto/connector/v1/credentialloader.proto`](../proto/connector/v1/credentialloader.proto) | The `.proto` defining `Ref`/`Material`/`Lease` messages and the `CredentialLoader` gRPC service (`Resolve`, `List`, `Lease`, `Renew`, `Revoke`, `Health`, `Capabilities`). Generated, committed Go stubs live in [`connectorpb/`](../connectorpb/) — a `buf generate` regenerates them; consumers need no local `protoc`. |
| Marshalling | [`transport/`](../transport/transport.go) | `RefToPB`/`RefFromPB`, `LeaseToPB`/`LeaseFromPB`, and `ErrToStatus`/`ErrFromStatus`, which map every `cerr.Kind` to a distinct `google.golang.org/grpc/codes` code and back — errors cross the wire as gRPC status, never as strings for the caller to pattern-match. |
| Transport binding | [`plugin/`](../plugin/plugin.go) | `plugin.Handshake` (the go-plugin magic-cookie handshake both sides must share), `plugin.CredentialLoaderName`, and `plugin.PluginMap(impl)`. A provider passes `plugin.PluginMap(impl)` to `goplugin.ServeConfig.Plugins`; the host's `Dispense(plugin.CredentialLoaderName)` returns a value that itself satisfies `credential.CredentialLoader` — from the host's point of view, calling a Track-B provider looks identical to calling a native connector. |
| Manifest | [`manifest/`](../manifest/manifest.go) | `connector.yml`, the file a provider ships alongside its binary declaring `name`, `implements` (a known `connector.Class`, e.g. `CredentialLoader`), `kind` (`declarative` \| `provider` \| `native`), `capabilities`, `supports`, refs-only `auth`, and — required for `kind: provider` — `min_host_version`, the minimum host contract version the provider requires. `manifest.Parse` is strict (unknown fields rejected); `Doc.Validate()` closes the `kind`/`implements` enums and rejects any `auth` entry that isn't an `op://` ref or a bare environment-variable name (never literal secret material). |

**Refs-only over the wire.** The `Resolve`/`Lease` RPCs take a `Ref` and
return only the `Material` the caller asked for — a provider never returns
unrequested material, and the wire carries raw bytes with no logging or
serialization step that could leak them. The host-side `grpcClient` re-wraps
every returned byte slice with `credential.NewMaterial`, so `String()`
redaction and `Zero()` wiping hold exactly the same as for a native connector
— see [`SECURITY.md`](SECURITY.md).

**Error strings are not redacted.** Unlike `Material`, an `error` a provider
returns crosses the wire verbatim: `transport.ErrToStatus`/`ErrFromStatus`
carry `err.Error()` as the gRPC status message, unmodified in both
directions, and a host may log a received error as part of its audit trail.
A connector author MUST NOT embed secret material in an error string — see
[`SECURITY.md`](SECURITY.md#track-b-error-strings-cross-the-wire-verbatim)
for the full rule.

**Reference provider.** [`examples/credentialloader-provider/`](../examples/credentialloader-provider/main.go)
is a complete, vendor-free `CredentialLoader` provider: a `memLoader` over a
fixed map of placeholder `op://` refs, served via `goplugin.Serve`. Copy
`main.go` as the starting point for a real provider (Infisical, Vault, ...) —
swap `memLoader`'s method bodies for calls to the actual backing store; the
`plugin.Handshake` + `plugin.PluginMap(...)` + `goplugin.Serve` wiring does
not change. [`roundtrip_test.go`](../examples/credentialloader-provider/roundtrip_test.go)
in the same directory builds that binary and drives it as a real subprocess
the way a host would — dial, `Dispense`, `Resolve`, `Kill` — confirming the
process actually exits (dies-with-session).

Out of scope here (a separate, later Story): the host-side registry, the
go-plugin dial + broker wiring, and a `secrets.Provider` adapter — this
module ships only the provider-side wire contract and a self-contained
reference implementation.
