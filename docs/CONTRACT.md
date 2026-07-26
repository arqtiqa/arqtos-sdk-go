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
| `Resolve(ctx, r ref.Ref) (Resolution, error)` | Resolves a single `op://<vault>/<item>/<field>` reference to its current secret material, returned as a [`Resolution`](#resolution-an-unresolved-credential-cannot-look-like-an-empty-one). This is the base operation — a `CredentialLoader` MUST support `Resolve` (gated by `CapRead`). Returns `cerr.KindNotFound` if the ref does not exist, `cerr.KindUnauthorized` if the caller lacks access (including a backend session that is not signed in), `cerr.KindInvalid` for a malformed ref, `cerr.KindRateLimited` when the backend refuses load, `cerr.KindUnavailable`/`cerr.KindTimeout` for transient backing-store failures. |
| `List(ctx, scope string) ([]ref.Ref, error)` | Lists the `ref.Ref`s visible under `scope` (a connector-defined scope string, e.g. a vault name). Returns refs only — never resolves material for the listed entries. A connector that cannot enumerate (e.g. a store with no listing API) returns `cerr.KindUnsupported`. |
| `Lease(ctx, r ref.Ref) (Resolution, Lease, error)` | Like `Resolve`, but for a backing store that issues **dynamic**, time-bounded secrets (e.g. a database credential broker): returns a `Resolution` plus a `Lease` describing its `TTL`/`ExpiresAt`/`Renewable`. Gated by `CapLease`; a connector without dynamic-secret support returns `cerr.KindUnsupported`. |
| `Renew(ctx, l Lease) (Lease, error)` | Extends a `Lease` obtained from `Lease(...)`, returning the renewed `Lease` (new `ExpiresAt`). Returns `cerr.KindInvalid` for an unknown/expired lease ID, `cerr.KindUnsupported` if `l.Renewable` is false or the connector lacks `CapLease`. |
| `Revoke(ctx, l Lease) error` | Proactively invalidates a `Lease` before its natural expiry (e.g. on session end — see [`docs/SECURITY.md`](SECURITY.md), "dies-with-session"). Idempotent: revoking an already-revoked or expired lease is not an error. |

### `Resolution`: an unresolved credential cannot look like an empty one

`Resolve` and `Lease` do not return a `*Material`. They return a
`credential.Resolution`, which has exactly three states and no way to confuse
the first two:

| State | How it is produced | What `Value()` does |
|---|---|---|
| unresolved | the zero `Resolution` — what `return Resolution{}, nil` produces | returns a `*FaultError`, never a value |
| present, empty | `credential.ResolvedEmpty()` — an explicit assertion | returns present material of length zero |
| present | `credential.Resolved(m)` with non-empty material | returns the material |

The pair *(empty value, no error)* is therefore **not constructible**:

- `credential.Resolved(m)` returns `(Resolution, error)` and **refuses** `nil`
  or zero-length material, handing back a `*FaultError` at the point of the
  mistake. A connector writes `return credential.Resolved(credential.NewMaterial(b))`
  and gets the guarantee without writing an emptiness check.
- The zero `Resolution` is unresolved, not empty. `Value()` refuses to read
  it, so there is no path from "the connector returned nothing" to "the
  credential is empty" — including for a caller that ignores the error, which
  then holds a `nil` `*Material` and panics loudly rather than authenticating
  with `""`.
- A secret whose value is **genuinely** empty is expressible, but only by
  saying so: `ResolvedEmpty()`. Call it only where the backend distinguishes a
  stored-empty value from an unauthenticated or failed read. Emptiness is
  asserted, never inferred from the bytes.

Why the contract carries this rather than leaving it to connector authors: a
signed-out `op environment read` prints **nothing and exits 0**. Every backend
has some version of it, and an author who has never met that failure has no
reason to guard against it.

**The guarantee is a construction-time one.** "Readable implies non-empty"
holds for a `Resolution` as it is built, not for the rest of its life, and one
operation breaks it deliberately: `Material.Zero()` wipes the bytes a
`Resolution` already holds, so a `Resolution` built from a real secret and then
`Zero()`ed reads back present-and-empty. That is intended — `Zero()` is the
dies-with-session wipe, and a caller reaching for it is saying "this material
is finished with". Hold a resolved value for as long as you need it, `Zero()`
it, and do not read through the same `Resolution` afterwards. What the type
guarantees is what a **connector** can hand you, not what you can do to it
later.

**Host side.** `credential.CheckResolution(name, op, res, err)` is the guard a
host runs on what a connector returned. A violation comes back as a
`*credential.FaultError` — a named fault (`FaultUnresolved`,
`FaultBatchMismatch`) attributed to the named connector, of kind
`cerr.KindContractViolation` — rather than being coerced into a generic error
that would leave the operator looking for the fault in the backend. Callers
never test a credential for emptiness; they call `Value()` and handle its
error, which is a question about the connector, not about the secret.

**Over the wire.** The same three states cross a Track-B boundary, and
emptiness is **asserted there too** — message presence alone cannot carry it:

| Wire | Meaning |
|---|---|
| no `Material` message | nothing was resolved |
| `Material` with `value` bytes | a value (the assertion flag is ignored) |
| `Material`, no bytes, `empty_by_assertion = true` | a deliberately-empty value |
| `Material`, no bytes, **no** assertion | **nothing was resolved** |

The last row is the load-bearing one. proto3 does not put a zero-length
`bytes` field on the wire at all, so a conformant `ResolvedEmpty()` and a
provider that resolved nothing and sent a default-constructed `Material`
serialize **byte-identically**. Reading "present, no bytes" as
deliberately-empty therefore hands the host an empty credential for a read
that produced nothing — the same conflation the contract refuses in-process,
reopened at the one boundary where the host cannot inspect the sender's code.
`empty_by_assertion` inverts which encoding is dangerous: what a confused or
hurried foreign author emits for "I got nothing" now means unresolved, which
is safe, and the hazardous meaning requires a deliberate opt-in.

The host-side stub runs `CheckResolution` on everything a provider sends, so
an out-of-process provider — someone else's binary, possibly not even Go —
cannot hand the host an empty credential: an unasserted empty `Material` comes
back as a `*credential.FaultError` naming that connector, not as a readable
`""`. [`proto/connector/v1/credentialloader.proto`](../proto/connector/v1/credentialloader.proto)
states these rules in the file itself, for authors who will never read the Go.

### Batch resolution (`CapBatchResolve`)

Batch resolution is an **optional** operation a connector **declares**:

```go
type BatchResolver interface {
	ResolveBatch(ctx context.Context, refs []ref.Ref) ([]BatchResult, error)
}

// One requested reference's outcome. Built only through a constructor, read
// only through an accessor — so "either a resolution or a failure" is a
// property of the type rather than a sentence in this document.
func BatchResolved(r ref.Ref, res Resolution) (BatchResult, error)
func BatchFailed(r ref.Ref, err error) (BatchResult, error)

func (b BatchResult) Ref() ref.Ref
func (b BatchResult) Resolution() Resolution
func (b BatchResult) Err() error
```

`BatchResult`'s fields are unexported for the same reason `Resolution`'s are.
A struct with three exported fields lets a connector fill in a resolution
**and** an error — two hosts then pick differently — or neither, which hands
the host a silent blank for a reference it asked about. Each constructor sets
exactly one outcome; asked for something that is neither (`BatchResolved` with
an unresolved `Resolution`, `BatchFailed` with a `nil` error) it returns a
`*FaultError` and a result carrying no outcome, so ignoring the error cannot
launder a blank into a value. `CheckBatch` catches whatever still gets
through, including the zero `BatchResult`.

A connector that can resolve many references in ONE backend call implements
`credential.BatchResolver` **and** declares `batch_resolve` — in its manifest
and from `Capabilities()`. Both, or conformance fails it: a declared
capability that is absent is worse than an undeclared one, because the host
plans one call and finds no operation to make it with.

`ResolveBatch` returns exactly one `BatchResult` per requested reference, **in
the requested order**, each carrying either a resolution or a typed failure.
The returned `error` is a failure of the batch call itself; a single missing
reference belongs in its own result, so it does not discard the other values
fetched with it. `credential.CheckBatch` is the host-side guard for that
correspondence.

Where the capability is absent, the host resolves one reference at a time
**and reports the degradation** — a silent fan-out is how a call quota gets
spent with the evidence pointing at the wrong component. The host-side
degradation and reporting are the host's half of this requirement, not the
connector's.

**Out-of-process providers batch too.** The Track-B service carries a
`ResolveBatch` RPC, so `batch_resolve` is a capability a `kind: provider`
connector can genuinely honour, not only a native one. The host-side stub
implements `credential.BatchResolver` **exactly when** the provider reports
`batch_resolve` from its `Capabilities` RPC, so a host discovers batch by type
assertion — identically for a provider and a native connector, with no second
code path. Per-result material follows the same `Material` presence rules,
`empty_by_assertion` included; a per-reference failure crosses as a code +
message that maps back into the same `cerr.Kind` vocabulary. The stub runs
`CheckBatch` on what comes back, so correspondence and per-result presence are
verified host-side before a host sees any of it.

A provider that reports `batch_resolve` without implementing
`credential.BatchResolver` answers `UNIMPLEMENTED` (`cerr.KindUnsupported`)
rather than fanning out to N single resolves behind the host's back — which is
how a quota disappears with the evidence pointing at the wrong component.

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
| `CapBatchResolve` | Resolves many references in ONE backend call, via `credential.BatchResolver`. MUST be declared in the manifest **and** by `Capabilities()`, and MUST be implemented — see [Batch resolution](#batch-resolution-capbatchresolve). |

`CapOIDC` and `CapAppRole` describe how the connector itself authenticates
outward, not a behavior it exposes inward — hosts use them to reason about the
connector's own credential posture, e.g. for audit and rotation policy.

A host MUST call `Capabilities().Has(cap)` before invoking a
capability-gated method, and a connector MUST return
`cerr.New(cerr.KindUnsupported, op, nil)` from any method whose capability it
does not advertise, rather than silently no-op'ing.

## The `cerr.Kind` taxonomy

Every error a contract method returns is a `*cerr.Error{Kind, Op, Err}`.
Callers classify with `cerr.KindOf(err)` / `cerr.Retryable(err)` /
`cerr.TripsBreaker(err)` — never by matching on the error string.

The vocabulary is **closed**: `cerr.Kinds()` is the whole set, `Kind.Valid()`
rejects anything outside it, and adding one is a deliberate change to a
published contract rather than a local edit. That closure is the point — a
backend that rewords an error must not be able to change host behaviour, and
three backends must not each grow their own string-matching dialect of the
same classification.

| `Kind` | Meaning | Retryable | Trips breaker |
|---|---|---|---|
| `KindUnknown` | The connector could not classify the failure — also what a plain error that never passed through `cerr.New` classifies as. | no | **no** |
| `KindNotFound` | The referenced secret, scope, or lease does not exist. | no | no |
| `KindUnauthorized` | The caller/connector identity lacks access — including a backend session that is not signed in. | no | no |
| `KindUnavailable` | The backing store is transiently unreachable (network, outage). | yes | no |
| `KindRateLimited` | The backend itself reported a quota or rate limit. | no | **yes** |
| `KindUnsupported` | The operation is not implemented by this connector/capability set. | no | no |
| `KindInvalid` | The input (a malformed `ref.Ref`, an unknown lease ID, ...) is invalid. | no | no |
| `KindTimeout` | The operation did not complete within its deadline. | yes | no |
| `KindContractViolation` | The connector returned something the contract does not admit. Host-detected — a connector never returns it. | no | no |

`cerr.Retryable(err)` is `true` exactly for `KindUnavailable` and
`KindTimeout` — the two kinds where retrying the same call, generally after a
backoff, may succeed without any change in caller behavior. `KindRateLimited`
is deliberately **not** retryable: a rate limit is not waited out by retrying
into it, it is withheld by the breaker.

### `Unknown` does not trip the breaker

`cerr.TripsBreaker(err)` is `true` for exactly one kind, `KindRateLimited` —
positive evidence, reported by the backend, that it is refusing load.

An unclassifiable failure is `KindUnknown`, and Unknown does **not** escalate.
That is the requirement, not an omission: a breaker opened on a guess converts
one unrecognised error into a total resolution outage for the backend it was
meant to protect, which is strictly worse than the rate limit it guards
against.

⚠️ This is the **opposite** default from version negotiation, where a
connector whose contract version cannot be verified is refused. Both are
correct — an unverifiable *connector* must not run, while an unclassified
*transient failure* must not escalate — and unifying them breaks one of them.

`cerr.Classified(err)` separates "the connector said Unknown" from "the
connector returned a bare error": `KindOf` answers `KindUnknown` for both, and
only the second is a conformance failure. The breaker treats them the same
way, which is what makes the safe default safe.

## Checking your connector: `credconform`

[`credconform`](../credconform/) runs the parts of this contract a compiler
cannot check, so you can gate your own CI on them before arqtos ever loads
your connector:

```go
rep, err := credconform.Run(ctx, myLoader, credconform.Options{
	Manifest:     myManifest,
	Resolvable:   []ref.Ref{presentRef},
	Unresolvable: absentRef,
})
if err != nil {
	return err // the check could not be run at all
}
if err := rep.Err(); err != nil {
	return err // the connector ran, and is not conformant
}
```

| Check | What it requires |
|---|---|
| `manifest/valid` | the manifest validates, declares this class, and declares only capabilities the class defines |
| `capability/manifest-matches-runtime` | the manifest's `capabilities` and the running connector's `Capabilities()` are the same set |
| `batch/declared-is-implemented` | `batch_resolve` is declared in both places exactly when `credential.BatchResolver` is implemented |
| `resolve/no-empty-success` | every reference the run declares resolvable comes back carrying **material** — not a success carrying nothing, and not a `ResolvedEmpty()` assertion either |
| `failure/typed` | the reference the run declares unresolvable fails with a classified `cerr.Kind` |
| `batch/results-match-request` | batch results correspond one-for-one, in order, with the request — reported only for a connector that implements batch |

Every field of `Options` is required. A fixture that is missing makes `Run`
return an error rather than skip the check it would have driven: a report that
is green because nothing looked is worse than no report.

`Resolvable` must point at references that hold **actual bytes**.
`ResolvedEmpty()` is a legitimate assertion about a secret an operator really
did store empty; it is not a legitimate answer for a reference you nominated
as proof that your connector resolves things. A presence-only check let a
connector answer *every* read that way — the move an author reaches for when
`Resolved()` refuses their signed-out backend — and score a fully green report
while serving `""` to every caller.

`Run` returns an `error` only when the run could not be carried out. A
connector that runs and is non-conformant yields a `nil` error and a `Report`
whose `Err()` is a `cerr` of kind `cerr.KindInvalid` naming the connector and
every failed check — so gate on `Report.Err()`, not on the returned error
alone. `Report.String()` renders one line per check for CI logs.

Each check is tested against a connector built to violate exactly the property
it checks. A harness only ever run against compliant input proves nothing
about what it would catch.

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
| Contract | [`proto/connector/v1/credentialloader.proto`](../proto/connector/v1/credentialloader.proto) | The `.proto` defining `Ref`/`Material`/`Lease`/`Failure` messages and the `CredentialLoader` gRPC service (`Resolve`, `List`, `Lease`, `Renew`, `Revoke`, `Health`, `Capabilities`, `ResolveBatch`). It carries the presence rules **in the file**, in comments, because it is the contract for authors who will never read the Go. Generated, committed Go stubs live in [`connectorpb/`](../connectorpb/) — a `buf generate` regenerates them; consumers need no local `protoc`. |
| Marshalling | [`transport/`](../transport/transport.go) | `RefToPB`/`RefFromPB`, `LeaseToPB`/`LeaseFromPB`, `ResolutionToPB`/`ResolutionFromPB` (which own the presence + `empty_by_assertion` rules), `BatchResultToPB`/`BatchResultFromPB`, and `ErrToStatus`/`ErrFromStatus`, which map every `cerr.Kind` to a distinct `google.golang.org/grpc/codes` code and back — errors cross the wire as gRPC status, never as strings for the caller to pattern-match. |
| Transport binding | [`plugin/`](../plugin/plugin.go) | `plugin.Handshake` (the go-plugin magic-cookie handshake both sides must share), `plugin.CredentialLoaderName`, and `plugin.PluginMap(impl)`. A provider passes `plugin.PluginMap(impl)` to `goplugin.ServeConfig.Plugins`; the host's `Dispense(plugin.CredentialLoaderName)` returns a value that itself satisfies `credential.CredentialLoader` — from the host's point of view, calling a Track-B provider looks identical to calling a native connector. |
| Manifest | [`manifest/`](../manifest/manifest.go) | `connector.yml`, the file a provider ships alongside its binary declaring `name`, `implements` (a known `connector.Class`, e.g. `CredentialLoader`), `kind` (`declarative` \| `provider` \| `native`), typed `capabilities` (`[]connector.Capability`, checked against the class vocabulary and against the running connector by `credconform`), `supports`, refs-only `auth`, and — required for `kind: provider` — `min_host_version`, the minimum host contract version the provider requires. `manifest.Parse` is strict (unknown fields rejected); `Doc.Validate()` closes the `kind`/`implements` enums, closes the `capabilities` vocabulary against the class in `implements` (so a misspelled capability is refused by the host **before** it loads anything, not only by a full `credconform` run against a live connector), and rejects any `auth` entry that isn't an `op://` ref or a bare environment-variable name (never literal secret material). |

**Refs-only over the wire.** The `Resolve`/`Lease`/`ResolveBatch` RPCs take
`Ref`s and return only the `Material` the caller asked for — a provider never
returns unrequested material, and the wire carries raw bytes with no logging
or serialization step that could leak them. The host-side `grpcClient` re-wraps
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

## Declarative connectors: the MCP protocol surface

A **declarative** connector ships no Go code. It points arqtos at an MCP
server and maps connector-class operations onto that server's tools. Its
contract is therefore the **MCP protocol itself**, not the Go interfaces
above — and arqtos tracks that protocol through the **official MCP Go SDK**
(`github.com/modelcontextprotocol/go-sdk`), which this module depends on at
the same version the host does. arqtos ships **no dual-protocol shim of its
own**: compatibility across protocol revisions comes from the SDK's
streamable-HTTP wrapper.

### What arqtos assumes about your MCP server

The MCP specification is moving to a **stateless core** — no server-held
session, no guaranteed `initialize` handshake between calls, and required
values carried in HTTP headers rather than recovered by inspecting payloads.
Two consequences bind a declarative connector:

1. **Do not depend on a session.** arqtos does not promise a long-lived
   connection. A server whose tool set, auth state, or cursor position is
   only correct inside the first session is unusable as a backend even if it
   works interactively.
2. **Statelessness is configured, not inferred.** If you serve over
   streamable HTTP with the Go SDK, you must set
   `mcp.StreamableHTTPOptions{Stateless: true}` explicitly. The default
   handler mints a session id for a request that arrives without one, and
   then rejects a client that skipped the handshake with *"method … is
   invalid during session initialization"*. "We keep no state, so we are
   stateless" is not enough — the option has to be set.

   A stateless handler also answers the `GET` that would open a standalone
   SSE stream with `405 Method Not Allowed`. That is correct: arqtos drives
   connector backends request/response and does not consume server-initiated
   messages outside a request it made.

### Checking your server: `mcpconform`

[`mcpconform`](../mcpconform/) runs those assumptions as checks, using the
official SDK as the client, so you can gate your own CI on them before arqtos
ever dials your endpoint:

```go
report, err := mcpconform.Run(ctx, mcpconform.StreamableHTTP(endpoint), &mcpconform.Options{
	RequireTools: []string{"list_items", "create_item"},
})
if err != nil {
	return err // the check could not be run at all
}
if err := report.Err(); err != nil {
	return err // the server answered, and is not conformant
}
```

| Check | What it requires |
|---|---|
| `initialize` | the server connects and negotiates a protocol version |
| `ping` | a ping round-trip succeeds |
| `tools/list` | at least one tool is exposed |
| `tools/required` | every tool named in `Options.RequireTools` is present (only when set) |
| `session-independent` | a `tools/list` carrying **no `initialize` and no `Mcp-Session-Id`** is answered with a result |
| `tools-stable-across-reconnect` | a **second, independent** connection presents the same tools as the first |

`Run` returns an `error` only when the check could not be carried out (a nil
or failing transport factory). A server that answers and is non-conformant
yields a `nil` error and a `Report` whose `Err()` is a `cerr` of kind
`cerr.KindInvalid` — so gate on `Report.Err()`, not on the returned error
alone. `Report.String()` renders one line per check for CI logs.

`Run` takes a transport **factory** rather than a single `mcp.Transport`
because a transport is consumed by a connection, and the reconnect check needs
a second connection that stands on its own.

#### The two are different properties, and the difference matters

`session-independent` is assumption 1 above. `tools-stable-across-reconnect` is
**not**, and must not be read as a proxy for it:

- The SDK's Go client **always** performs `initialize`. Every connection it
  opens is a handshaken session, so any check built on it compares sessions to
  each other — it cannot observe whether the server needs the handshake at all.
- `session-independent` therefore POSTs the JSON-RPC call directly, with no
  handshake behind it and no session header on it. That is what a stateless
  client looks like on the wire, and what arqtos sends when it has no session
  to reuse.

A server built with `Stateless: false` — the SDK's default — **passes**
`tools-stable-across-reconnect` (its tool set really is stable) and **fails**
`session-independent`. An earlier revision of this package had only the former,
under the name `stateless-reconnect`, and consequently passed such a server on
the very property it fails. The rename is deliberate: the reconnect check
measures something real and is kept, but it no longer stands in for
session-independence.

`SessionIndependence` is exported so you can run just that probe against a
deployed endpoint:

```go
if res := mcpconform.SessionIndependence(ctx, endpoint, nil); !res.Pass {
	return fmt.Errorf("%s: %s", res.Name, res.Detail)
}
```

For a transport that is not streamable HTTP there is no endpoint to POST to, so
the check reports `not verified` — and that counts as a **failure**, not as an
omission. A conformance report must not be green because nothing looked.

### Protocol-version compatibility

Compatibility is the SDK's job. The tests alongside `mcpconform` pin the
behaviour at the JSON-RPC level, against one handler instance, so a version
bump cannot quietly change it:

- a **previous-protocol** client (`2025-03-26`) performs the handshake and is
  negotiated down to its own version;
- a **stateless** client sends `tools/list` and `tools/call` with **no
  `initialize` and no session id** and is served by the same handler;
- a client that sends no `Mcp-Protocol-Version` header at all is assumed to
  be `2025-03-26` — the last revision predating the header;
- an unrecognised version is rejected with `400 Bad Request`.

⚠️ **The stateless-core revision (`2026-06-30`) is not yet negotiable.** The
pinned SDK implements that revision's standard request headers (`Mcp-Method`
/ `Mcp-Name`, validated against the body) but does not list `2026-06-30`
among the versions it accepts, so a client announcing it is refused with
`400` before those headers are ever checked. A test pins this deliberately:
when it starts failing, the SDK has begun accepting the revision, and the
header requirements need re-verifying against arqtos MCP surfaces.
