# The arqtos connector contracts

This document describes the method-by-method semantics of each **connector
class** the SDK publishes, the capability set each is gated by, and the
[`cerr`](../cerr/cerr.go) error taxonomy — shared by every class — that every
contract method returns errors in.

| Class | What it adapts | Contract | Conformance harness |
|---|---|---|---|
| [`CredentialLoader`](#credentialloader-methods) | a secret store | [`credential`](../credential/credential.go) | [`credconform`](../credconform/) |
| [`Roster`](#the-roster-contract) | a directory of people and groups (read-only) | [`roster`](../roster/roster.go) | [`rosterconform`](../rosterconform/) |
| [`CodeCI`](#the-codeci-contract) | a code host's PR/CI surface | [`codeci`](../codeci/codeci.go) | [`codeciconform`](../codeciconform/) |
| [`Tracker`](#the-tracker-contract) | one work tracker — one board on one instance of one provider | [`tracker`](../tracker/tracker.go) | [`trackerconform`](../trackerconform/) |
| [`Authenticator`](#the-authenticator-contract) | an identity provider, for establishing who is driving this session | [`authenticator`](../authenticator/authenticator.go) | [`authconform`](../authconform/) |

`CredentialLoader`, `Roster` and `Authenticator` are implemented by **native**
(in-process, compiled into the host) connectors, and each is also implemented by
**out-of-process** (Track-B) connectors — see
[Track-B: the out-of-process wire contract](#track-b-the-out-of-process-wire-contract).

`CodeCI` and `Tracker` are native-only today: neither carries a `.proto` or a
Track-B wire binding, the same position the other three each started from before
their own wire protocol landed as a separate piece of work.

The set of classes is **closed**: `connector.Classes()` is the whole list,
`Class.Valid()` rejects anything outside it, and the `connector.yml`
`implements` enum is *derived* from that same list rather than restating it — so
a class the SDK knows is always declarable in a manifest, and a class it does
not know is refused before a host loads anything.

## The `Authenticator` contract

Establishes **who is driving this session**, interactively and verifiably. It is
deliberately distinct from `Roster`, which reads a directory with a service
credential and answers *who exists*. A failed directory read is a stale roster;
a failed authentication is **a session that must not start**.

| operation | contract |
|---|---|
| `Begin(ctx) (Challenge, error)` | Starts an exchange and returns the authorization URL plus an **opaque handle**. The connector generates and retains the PKCE verifier and nonce against that handle. It does NOT open a browser and does NOT bind a listener. |
| `Complete(ctx, handle, code) (Assertion, error)` | Exchanges the code the host received on its own redirect, **verifies before returning anything** — signature against the provider's key set, issuer, audience, the nonce issued with this handle, and expiry — and returns the [`Assertion`](#the-assertion). Any check failing is a `VerificationError`, never an assertion. An unknown, consumed or expired handle is `cerr.KindInvalid`. |

⚠️ **The host opens the browser and owns the loopback redirect listener.** The
connector holds the vendor knowledge and does the verification. This is not a
wiring preference: contract invariant 2 is passthrough-prohibited, and a
connector that obtained the operator's token and handed it onward would be
brokering a credential for a third party. The single credential-shaped value
crossing host to connector is the **authorization code** — single-use,
short-lived, PKCE-bound, and useless without the verifier the connector holds.
**Nothing obtained from the exchange crosses back.**

### The `Assertion`

`{PrincipalID, Authenticated, Active}`, and **nothing else**. There is no field
for a token and none for a raw claim set, so passthrough is *unrepresentable*
rather than merely discouraged — pinned by a test over the struct's fields.

`Coherent()` is the single invariant: `Authenticated == (PrincipalID != "")`.
Both incoherent shapes are refused — an authenticated assertion naming nobody
cannot be acted on, and a principal named while denying authentication is a name
nothing verified.

`Active: false` with `Authenticated: true` is a **real answer** — a verified
principal who is suspended or mid-transfer — reported as-is, never as an error
and never defaulted.

### Verification failures

Five distinguishable reasons — bad signature, wrong issuer, wrong audience,
replayed nonce, expired — all classifying as `cerr.KindUnauthorized`.
Distinguishable for the **operator**, who needs to know which check said no;
uniform for the **host**, because all five have the same routing answer: do not
retry, do not trip the breaker, do not start the session.

⚠️ **A rejected assertion is never `Authenticated: false`.** That is a permitted
answer only when the provider *answered* and identified the caller as nobody.

⚠️ **One shape no guard can catch, stated so nobody believes it does.** A
connector whose verification failed and which returns an anonymous assertion
with a **nil** error is, from outside, identical to one reporting a genuine
anonymous answer. The difference is entirely inside the connector, so the
obligation rests there and is caught by the conformance harness driving a stub
built to violate it.

### Capabilities

**Empty at v1, deliberately.** A device-code path, a machine-principal path, a
refresh path and a step-up path were each identified and not added, because a
capability nothing gates on is a speculative feature flag. The manifest closes
the vocabulary against that empty set, so a capability declared for this class is
refused.

## The base contract

Every connector, regardless of class, implements
[`connector.Connector`](../connector/connector.go):

| Method | Semantics |
|---|---|
| `Implements() Class` | Returns the connector class this instance implements — `connector.ClassCredentialLoader` for a `CredentialLoader`, `connector.ClassRoster` for a `Roster`. Used by the host to route by class without a type assertion. |
| `Capabilities() Capabilities` | Returns the set of optional behaviors this instance supports (see each class's capability vocabulary: [`CredentialLoader`](#credentialloader-capabilities), [`Roster`](#the-roster-capability-vocabulary)). The host MUST check `Capabilities().Has(...)` before calling an optional method, and a connector MUST return `cerr.KindUnsupported` from any method it advertises as unsupported. |
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
cannot make a read that resolved nothing look like a successfully-empty
credential: an unasserted empty `Material` comes back as a
`*credential.FaultError` naming that connector, not as a readable `""`. A
provider **can** still hand the host a genuinely empty credential, the same
way an in-process connector does — by asserting it (`empty_by_assertion =
true`) — but that assertion is the only path to one; omission is not.
[`proto/connector/v1/credentialloader.proto`](../proto/connector/v1/credentialloader.proto)
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

## `CredentialLoader` capabilities

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

### Reporting a rate limit rather than guessing at one

`KindRateLimited` is the one kind that must come from **positive evidence** — the
backend saying so — because it is the one kind that opens a breaker. Reading that
evidence off a real backend is more work than it looks: GitHub alone signals
three separate limits three separate ways, and one of them is not a status code
or a header at all.

A GitHub-backed connector does not need to re-derive that.
[`githubratelimit`](../githubratelimit/) reads all three, distinguishes them, and
returns a `*githubratelimit.Error` that unwraps to `KindRateLimited` — so a host
classifying with `cerr.KindOf` / `cerr.TripsBreaker` gets the right answer
without knowing the type exists. See
[Handling GitHub rate limits](../README.md#handling-github-rate-limits).

Nothing in the contract requires it. A connector against another backend
classifies its own quota refusals as `KindRateLimited` the same way, and the
rule is unchanged: report it on evidence, never on a guess about an error the
connector did not recognise — that is what `KindUnknown` is for.

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

## The `Roster` contract

A **roster connector** adapts one directory — an identity provider, a workspace
directory, a code host's teams, a flat file checked into a repo — and reports
what that directory says about **principals**, **groups**, and the
**memberships** between them.

It is **read-only**. There is no create, no update, no delete, and none is
coming. arqtos provisions arqtos-side artifacts; it does not create people in
somebody else's directory, and a class that could write to one would make every
reconcile bug a destructive one.

### It reports directory facts, never arqtos concepts

A roster connector holds **no arqtos policy**. There is no org in these types,
no team, no role and no entitlement — mapping directory facts onto arqtos's own
organisational model is the **host's** job, on the host's side of this
boundary.

That is not tidiness. A connector that answered *"this person belongs to venture
X with role Y"* would have encoded the host's organisational model inside a
vendor adapter, and the host would then have as many organisational models as it
has directories, each drifting independently, each only correctable by whoever
maintains that adapter.

| Type | Fields | Notes |
|---|---|---|
| `Principal` | `ID`, `Handle`, `Email`, `DisplayName`, `Active`, `Kind` | `ID` is the directory's **stable** identifier — **not** the email. `Email` MAY be empty. `Active` is reported for **every** principal (see below). |
| `Group` | `ID`, `Handle`, `DisplayName`, `ParentIDs` | `ParentIDs` carries nesting where the directory supports it, and is empty for a flat directory. |
| `Membership` | `PrincipalID`, `GroupID`, `Direct` | `Direct: false` means inherited through group nesting. |

**`Principal.ID` is not the email.** Email changes, and a host that keyed on it
treats a renamed person as a departed person plus a new hire — the first half of
which is a full deprovision. Where a directory offers a numeric or opaque id
alongside a login, the id is what belongs in `ID`.

**`Principal.Kind`** is `human`, `machine`, or `unknown`, and its **zero value
is `unknown`**. A connector that forgets to set it reports "unclassified" rather
than accidentally reporting "human", so a host that treats service identities
differently knows it may apply neither rule.

**`Membership.PrincipalID` need not name a principal from `ListPrincipals`.**
Some directories admit groups as members of groups, and external or guest
identities a directory read does not enumerate. A host resolves what it
recognises and reports what it does not; it must **not** treat an unrecognised
member as an absent one.

### `Roster` methods

`Roster` embeds `connector.Connector` and adds three read operations:

| Method | Semantics |
|---|---|
| `ListPrincipals(ctx) (Resolution[Principal], error)` | Every identity in the directory this connector serves, **including deactivated ones**. |
| `ListGroups(ctx) (Resolution[Group], error)` | Every group in the directory this connector serves. |
| `ListMemberships(ctx, groupID) (Resolution[Membership], error)` | The memberships of the single group named by `groupID`. Every returned `Membership.GroupID` MUST equal `groupID`. A group that does not exist is `cerr.KindNotFound` — **not** an empty roster. |

`ListMemberships` is **per-group** because that is the shape every directory
actually offers: each backend this class was measured against exposes
members-of-a-group, and none exposes a whole-directory membership dump. A host
that wants the estate iterates `ListGroups` and calls `ListMemberships` for
each.

A result for a group other than the one asked about cannot be attributed by the
host, and guessing the correspondence is how the wrong people end up in the
wrong group. `roster.CheckMemberships` is the host-side guard that proves it —
the membership analogue of `credential.CheckBatch`.

### ⚠️ Suspended is **not** absent

A deactivated directory user is **still in the directory**. Report them, with
`Active: false`.

Omitting them tells the host they left the organisation, and the host revokes
everything — for somebody who is on parental leave. **Omission from
`ListPrincipals` means "not in the directory at all", and nothing else.**

The mirror-image error comes from the same missing field: a connector that
reports a deactivated identity as `Active: true` leaves a suspended account
holding everything it had. `rosterconform` checks both directions against a
principal you nominate as deactivated.

### `Resolution`: an unresolved roster cannot look like a roster of nobody

The three list operations do **not** return a slice. They return a
`roster.Resolution[T]`, which has exactly three states and no way to confuse the
first two:

| State | How it is produced | What `Items()` does |
|---|---|---|
| unresolved | the zero `Resolution` — what `return Resolution[Principal]{}, nil` produces | returns a `*FaultError`, never a list |
| present, empty | `roster.EmptyRoster[Principal]()` — an explicit assertion | returns a present list of length zero |
| present | `roster.Resolved(items, roster.Complete)` with a non-empty list | returns the list |

**Why the contract carries this.** A `[]Principal` of length zero is
*ambiguous*: it means either *"this directory genuinely has nobody"* or *"the
read failed and I am returning a zero value"*. The second reading is the
dangerous one because of what the host does next — an offboarding sweep computes
"in arqtos but no longer in the directory" and revokes it. Fed an empty list
that meant *the read failed*, that sweep **deprovisions the entire estate**,
correctly according to everything it was told.

This is deliberately the **same shape** as `credential.Resolution` rather than a
second invention; the blast radius is what differs. A credential that resolves
to `""` fails one authentication. A roster that reads as nobody removes
everyone's access at once.

So the pair *(empty list, no error)* is **not constructible**:

- `roster.Resolved(items, completeness)` returns `(Resolution[T], error)` and
  **refuses** a nil or empty list, handing back a `*FaultError` at the point of
  the mistake. The `Resolution` returned alongside the error is unreadable, so a
  connector that ignores the error still cannot launder an unread directory into
  an empty one.
- The zero `Resolution` is unresolved, not empty. `Items()` refuses to read it,
  so there is no path from "the connector returned nothing" to "the directory is
  empty".
- A directory that genuinely holds nothing of the requested kind is expressible,
  but only by saying so: `EmptyRoster[T]()`. Call it only where the backend
  distinguishes an empty result from an unauthenticated, throttled, misdirected
  or failed read. It is a **real state** — a newly created group has no members,
  and a host must be able to see that and remove the access that came with it —
  but it is not somewhere to put an error.

**There is no `Len()` and no `IsEmpty()`.** One accessor, on purpose: a `Len()`
that answered `0` for an unresolved `Resolution` would put the ambiguity
straight back, and every caller that branched on it before reading would again
be guessing which of the two things a zero meant. Ask `Items()`, handle its
error, take `len()` of what it gives you.

**The list is copied in both directions.** `Resolved` copies what it is given,
so a connector that reuses or truncates its own slice afterwards cannot
retroactively change what the host was handed; `Items()` returns a copy, so a
host that sorts or annotates in place cannot change what the next reader sees.
`Group.ParentIDs` — the only slice-valued field across the three Roster types —
is copied too, on both sides: a plain top-level copy of a `[]Group` copies each
`Group` by value, which copies the `ParentIDs` slice *header* without copying
the backing array it points at, so without the extra copy a host sorting
`ParentIDs` in place would still reach back into the connector's own slice.

**`String()` reports the state and the count, never the records.** A resolved
`Resolution` holds personal data about identifiable people, and it travels
through host code that logs. How many were read is diagnosis; who they are is
not — reaching the records requires the explicit `Items()` call.

### A truncated read cannot look like a complete one either

`roster.Resolved` takes a second argument, `roster.Completeness`, on **every**
call — `roster.Complete` or `roster.Partial` — with no default:

```go
func Resolved[T any](items []T, c Completeness) (Resolution[T], error)
```

**Why.** `ListPrincipals` is documented to paginate internally for a directory
of any real size, and a real pagination loop fails partway at least once in
production — a 429 on page 7 of 250, a timeout on page 40 of 40. Before this
existed, the line an author naturally wrote at that point was
`return Resolved(itemsReadSoFar)`: readable, non-empty, and indistinguishable
from a complete read to everything downstream. The blast radius is the **same
one** that justifies `Resolution` itself — an offboarding sweep run against a
principal list truncated after a failed page revokes everyone past the failure
point — and it is **more likely to occur** than a wholesale empty read, because
pagination fails in the middle far more often than a read fails to start at
all.

So a truncated read gets the same treatment an empty one already does:

- `roster.Complete` is the ordinary case — an atomic read, or pagination that
  ran to its own natural end — and behaves exactly as `Resolved` always did.
- `roster.Partial` asserts the list is only what was read before the operation
  stopped. `Resolved` **refuses to build a readable `Resolution` for one**, the
  same way it refuses an empty list: a host's reconcile loop cannot safely
  revoke access for a principal it never got to examine, so a read that stopped
  early must surface as a typed failure (`cerr.KindUnavailable`,
  `cerr.KindTimeout`, ...), never as a smaller success.

A partial read is therefore **not merely discouraged — it is impossible to
express as a success at all.** What this cannot catch, and does not claim to:
a connector that asserts `roster.Complete` on a list it knows to be truncated.
That is a conscious misclassification at the call site, not a shape any check
can tell apart from the truth from the outside; the fix narrows the mistake
from "the natural, careless thing to write" to "a lie", it does not claim to
catch the lie.

**Host side.** `roster.CheckResolution(name, op, res, err)` is the guard a host
runs on what a connector returned; `roster.CheckPrincipals` and
`roster.CheckMemberships` add the capability-aware checks below. A violation
comes back as a `*roster.FaultError` — a named fault attributed to the named
connector, of kind `cerr.KindContractViolation` — rather than being coerced into
a generic error that would leave the operator looking for the fault in the
directory.

| `Fault` | Meaning |
|---|---|
| `FaultUnresolved` | success carrying no list |
| `FaultPartial` | success asserting `roster.Partial` — a truncated read reported instead of a typed failure |
| `FaultMembershipMismatch` | a membership for a group other than the one requested |
| `FaultUndeclaredMachinePrincipal` | a machine principal from a connector that did not declare it can see them |
| `FaultUndeclaredInheritedMembership` | an inherited membership from a connector that did not declare nesting |

### The `Roster` capability vocabulary

Every entry is a **measured difference between real directories**, not a
speculative feature flag. Each is declared — in the connector's manifest **and**
by `Capabilities()` — and checked against the running connector by
`rosterconform`.

| Capability | Wire name | Why it exists |
|---|---|---|
| `CapWatch` | `watch` | One major directory lets an application subscribe to membership change; another's change-notification surface covers users only and explicitly excludes groups and their members. Optional operation: `roster.Watcher`. |
| `CapTransitiveMembership` | `transitive_membership` | One directory returns derived members in the same call as direct ones; another's provisioning path cannot represent group nesting at all. A host that assumed transitivity would under-read the second and conclude that people had left groups they are still in. |
| `CapMachinePrincipals` | `machine_principals` | One vendor's service applications are directory objects a read returns; another vendor's service accounts live in a separate cloud-IAM system and do **not** appear in the directory at all. A connector for the second genuinely cannot report machine principals, and must say so. |

Two of these are enforced **against the data**, in both directions:

- A connector **without** `machine_principals` MUST report no principal with
  `Kind: machine`; one **with** it must be able to show one.
- A connector **without** `transitive_membership` MUST report only direct
  memberships; one **with** it must be able to show an inherited one.

The "must not report it undeclared" half is what makes the *absence* readable. A
host reads "no machine principals" as a fact about the **directory** only when
the connector has said it can see them; without the declaration the host cannot
tell that from "this connector cannot see them", so an undeclared one makes both
readings wrong at once.

**`CapWatch` is an optimisation, not a correctness mechanism.** Correctness comes
from the host's reconcile loop, which re-reads the three lists on a schedule
whether or not any event arrived. A connector without `watch` is not a degraded
connector; it is one the host polls. Accordingly a `roster.Change` carries **no
before/after state** — only what to go and re-read. Carrying the change itself
would make the event stream a second source of truth, and a host applying deltas
is one dropped, reordered or duplicated event away from a roster that disagrees
with the directory, with no way to notice.

```go
type Watcher interface {
	Watch(ctx context.Context) (<-chan Change, error)
}
```

The returned `error` is a failure to *establish* the watch. Once established the
channel closes when `ctx` is done or when the connector can no longer sustain
the subscription — and a closed channel is not an emergency, which is why there
is no error channel: a host that loses its watch falls back to the poll it was
always doing.

### Checking your connector: `rosterconform`

[`rosterconform`](../rosterconform/) runs the parts of this contract a compiler
cannot check, so you can gate your own CI on them before arqtos ever loads your
connector:

```go
rep, err := rosterconform.Run(ctx, myRoster, rosterconform.Options{
	Manifest:           myManifest,
	Group:              populatedGroupID,
	AbsentGroup:        noSuchGroupID,
	SuspendedPrincipal: deactivatedPrincipalID,
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
| `watch/declared-is-implemented` | `watch` is declared in both places exactly when `roster.Watcher` is implemented, **and** — when it is — that calling `Watch` actually establishes one: a non-nil channel with no error, observed **open before** its context is cancelled, that then closes within a bounded time of cancellation |
| `machine-principals/declared-is-reported` | a principal with `Kind: machine` appears exactly when `machine_principals` is declared |
| `transitive-membership/declared-is-reported` | an inherited membership appears exactly when `transitive_membership` is declared |
| `lists/resolve-not-empty` | all three lists come back **readable** — never a success carrying nothing, and never one asserting `roster.Partial` — and the principal list **and** the group list carry **actual entries**, not an `EmptyRoster()` assertion |
| `principals/suspended-is-present-not-absent` | the principal you nominate as deactivated is reported, with `Active: false` |
| `memberships/match-requested-group` | every membership is for the group that was requested, and there is at least one |
| `failure/typed` | the group you nominate as absent fails with a classified `cerr.Kind` |

Every check runs on **every** run. None is skipped for a connector that lacks a
capability, because a skipped check and a passing check look identical in a
report.

Every field of `Options` is required. A fixture that is missing makes `Run`
return an error rather than skip the check it would have driven — a report that
is green because nothing looked is worse than no report. In particular:

- **`Group` must be populated.** An empty group is a real state and a conformant
  connector reports one, but a membership list with nothing in it proves nothing
  about whether what came back corresponds to what was asked. Where you declare
  `transitive_membership`, nominate a group that also has an **inherited**
  member.
- **`AbsentGroup` must not exist** — not merely be empty. *"This group has no
  members"* and *"there is no such group"* lead a reconcile loop to opposite
  conclusions, and the first one removes the access the group carried.
- **`SuspendedPrincipal` must be genuinely deactivated.** A run without one
  cannot see whether suspended people survive the read, and that is the most
  destructive failure this contract admits.

#### ⚠️ In-process is not the same run

`Run` takes a `roster.Roster`, so it works unchanged against a host stub talking
to a subprocess — but it **cannot tell**, and a report from it records
`TransportUnrecorded` rather than claiming the wire was exercised. If you ship
`kind: provider`, run `rosterconform.RunOutOfProcess` as well:

```go
rep, err := rosterconform.RunOutOfProcess(ctx, rosterconform.Provider{
	Path:        pathToYourBuiltProviderBinary,
	HostVersion: yourHostContractVersion,
}, opts)
```

It launches the binary, dials it exactly as a host does — including the
`min_host_version` negotiation — and runs every check above across a real gRPC
boundary. The entire class of marshalling failure is invisible to an in-process
run, because in-process nothing is marshalled: an unresolved read arriving as an
empty directory, a suspended principal losing its `Active` flag, a membership
arriving for a group nobody asked about. `TestAConnectorThatOnlyPassesInProcessFailsOverTheWire`
in [`rosterconform/outofprocess_test.go`](../rosterconform/outofprocess_test.go)
is that gap made falsifiable: one connector, green in-process, red across the
wire, with exactly one check flipping.

The exec lives here rather than in your own tests on purpose: a connector
repository forbids `os/exec` in its own package tree, so it cannot spawn its own
binary. The harness does it instead.

A returned error means the run could not be **carried out** — no path, a binary
that will not launch, or a provider the negotiation refused. A refused provider
is not a conformance failure: it was never dialled, so there is nothing to
report checks about.

#### Presence is not substance

`lists/resolve-not-empty` demands that the principal list hold principals AND
that the group list hold groups, not merely that the read succeeded. A
connector can answer every list with `EmptyRoster()` — present, readable,
nothing in it — and that is exactly the move an author reaches for when
`Resolved()` refuses their failing backend: it makes the error go away without
making the directory readable. Checked for presence only, such a connector
scores a **fully green report** while telling the host that the entire
organisation has left **and** that nothing is grouped.

Requiring groups specifically cannot register a false positive against a
directory that genuinely has none — but not because `Run` verifies
`Options.Group` names a real or populated group. It does not, and cannot, from
a string alone: `Run` refuses only an **empty** one. What actually rules it out
is what a connector fed a nonexistent `Options.Group` hits first: `ListMemberships`
for a group that does not exist must fail typed (`cerr.KindNotFound`, never an
empty roster), which fails `lists/resolve-not-empty` at the `ListMemberships`
stage — before the group-count requirement above is ever reached — and fails
`memberships/match-requested-group` the same way. A connector that instead
answers the nonexistent group with a readable-but-empty membership list is
still caught, by that same check's separate requirement that the nominated
group have at least one member.

`memberships/match-requested-group` also **correlates** `Options.Group` against
the groups `ListGroups` itself reported, so a connector that fabricates
correctly-shaped, correctly-attributed members for a group id its own
`ListGroups` never claims exists is refused **by name**, not by the coincidence
of the two failure paths above lining up. That correlation is what makes the
no-false-positive property hold **by construction**; the two paths above only
made it hold incidentally, and held before this fix too.

Note also what does *not* save a connector that drops suspended people: its list
is non-empty, every entry is well-formed, and the resolve check is green.
Substance in aggregate says nothing about whether the one entry that matters
survived, which is why the suspended principal is nominated by name.

The same "presence is not substance" gap existed for `watch/declared-is-implemented`
before it called `Watch`: a Go type assertion proves the method exists, and
nothing about what calling it does. A connector whose `Watch` always answers
`(nil, cerr.KindUnsupported)` — the stub an author reaches for while a real
subscription is still unimplemented — or `(nil channel, nil error)` passed a
type-assertion-only check. The first tells a host expecting push notification
nothing at all while it waits forever believing an event will arrive; the
second is worse — a host that ranges over a nil channel blocks forever and
never reconciles again. The check now calls `Watch` with a cancellable
context and requires a non-nil channel with no error, then cancels and
requires the channel to close.

Cancelling **first** and checking for closure only afterward cannot, by itself,
tell that apart from a channel that was already closed the moment `Watch`
returned: `ch := make(chan Change); close(ch); return ch, nil` — the most
idiomatic-**looking** of the three stubs above — passes a cancel-then-wait
check while establishing no subscription at all. So the check also takes a
**non-blocking receive before cancelling**: a receive on a closed channel is
always immediately ready, so this catches the already-closed stub without ever
blocking on a healthy one.

#### The declaration checks are not tautological

A declared-is-implemented check is worthless if "implemented" is computed from
the same signal as "declared" — the check then agrees with itself whatever the
connector does, and reports PASS on both of the two failures it exists to catch.
That is not hypothetical: it happened in this SDK's `credconform` over an
out-of-process provider, where the host-side stub's shape was derived from the
very capability declaration the check was verifying.

It cannot happen here, and both reasons are load-bearing:

- "Implements `roster.Watcher`" is a Go type assertion against the connector's
  own type, which no declaration can influence. That holds across the Track-B
  boundary too, by construction rather than by luck: **watch has no RPC on that
  wire**, so a dispensed host stub never satisfies `roster.Watcher` whatever the
  provider declares.
- The two behavioural capabilities are judged from **the data the connector
  returned** — a `Kind` of machine, a `Direct` of false — and data is not derived
  from a manifest either. That is what keeps them independent over the wire,
  where the host stub *does* read the capability RPC (it needs the declaration
  to apply the host-side guards): the declaration reaches the guards, never the
  judgement.

The tests drive each of these checks through all four combinations of *(declared,
actually the case)* and pin the verdict of each. A check reading one signal twice
can only produce two distinct verdicts across those four inputs, so the truth
table is the proof; the comment is not. **Whoever gives watch an RPC must not
derive the stub's `Watcher`-ness from the capability RPC**, or that proof
silently stops meaning anything — probe the provider's behaviour instead.

Each check also has a test driving it with a connector built to violate exactly
the property it checks — one reporting an unresolved list as a success, one
asserting `roster.Partial` on a truncated read, one reporting no groups at all
while principals and memberships are real, one whose `Watch` declares and
implements the method but never actually establishes a subscription, one
dropping suspended people, one whose manifest declares a watch it does not
implement, one returning memberships for the wrong group. A harness only ever run
against compliant input proves nothing about what it would catch.

#### A truncated read needs no separate check

`roster.Resolved` refusing to build a readable `Resolution` when asserted
`roster.Partial` (see [above](#a-truncated-read-cannot-look-like-a-complete-one-either))
means a connector reporting one this way returns a `*roster.FaultError`, which
`lists/resolve-not-empty` and `memberships/match-requested-group` already treat
exactly as they treat any other resolution failure. This package adds no
separate check for it: the type-level fix is what closes the hole, not
something a check could only describe after the fact. What no check anywhere
can catch is a connector that asserts `roster.Complete` on a list it knows to
be truncated — that is a deliberate misclassification at the call site, not a
shape a harness can distinguish from the truth from the outside.

### What a `Roster` connector does not do

- **No writing to a directory.** Read-only, permanently.
- **No reconcile loop.** That is the host's. This contract is only what the loop
  consumes.
- **No push notification out-of-process.** `CapWatch` has no RPC on the Track-B
  wire, so a `kind: provider` roster connector cannot declare it and is polled.
  Polling is the fallback that capability's contract already defines for a
  connector without it: correctness lives in the host's reconcile loop either
  way, and an event only makes it converge sooner.

## The `CodeCI` contract

A **codeci connector** adapts one code host's pull/merge-request and CI
surface — GitHub's PRs/checks/workflow-runs, GitLab's merge requests/pipelines.
It is deliberately a **separate class** from code-host administration (repo
creation, webhooks, runner tokens, git remotes): same vendor, different
contract, so an org can pair either half with a different backend or a
different token.

`CodeCI` is derived from **two** vendors' APIs, not one — an ABC written
against a single backend reliably encodes that backend's accidents as
contract. It is written against GitHub's current, in-production PR/CI surface
and against GitLab's documented REST API; it has not yet been proven against a
running GitLab connector, so treat the required methods as confident, not
closed.

### `CodeCI` methods

`CodeCI` embeds `connector.Connector` and adds:

| Method | Semantics |
|---|---|
| `WhoAmI(ctx) (Identity, error)` | The account this connector's credential authenticates as, as the **code host** reports it. `Identity` is `{Login, Authenticated}`, and it MUST be `Coherent()`: an authenticated identity carrying an empty login is a typed failure (`cerr.KindUnauthorized`/`KindUnavailable`), never a success. Required, not capability-gated — see [why](#why-whoami-is-required-and-not-behind-a-capability). |
| `CreatePR(ctx, req CreatePRRequest) (PR, error)` | Opens the pull/merge request described by `req` — `{FullName, Branch, Base, Title, Body, Draft}` — including a **draft** when `req.Draft` is set. MUST call `req.Validate()` before anything else and return its error unchanged: a struct lets a required field be forgotten where a positional argument forced an explicit `""`. |
| `ListPRs(ctx, fullName, state) (Resolution[PR], error)` | Every pull/merge request in `state`, paged to completion. `state` must be `PRState.UsableAsFilter()`; `PRStateUnspecified` is `cerr.KindInvalid`. |
| `MergePR(ctx, fullName, prID, method) error` | Merges `prID`. MUST validate `method.Specified()` **before** doing anything else, and MUST refuse a pull/merge request currently reported as a draft — both `cerr.KindInvalid`, both before any merge is attempted. |
| `GetDiff(ctx, fullName, prID) (Resolution[DiffFile], error)` | Every changed file in `prID`, paged to completion. A real pull/merge request always has at least one changed file, so `EmptyList` is never a legitimate answer here. |
| `ListBranches(ctx, fullName) (Resolution[Branch], error)` | Every branch, paged to completion. |
| `GetCheckRuns(ctx, fullName, ref) (Resolution[CheckRun], error)` | Every check/status entry against `ref`. A ref with no CI configured is a genuine, expressible empty result. |
| `GetWorkflowRun(ctx, fullName, runID) (WorkflowRun, error)` | One CI automation run (a GitHub Actions workflow run, a GitLab pipeline). One that does not exist is `cerr.KindNotFound`. |
| `GetIssue(ctx, fullName, number) (Issue, error)` | The issue numbered `number` in `fullName` — **board-agnostic by construction**. ⚠️ An issue that does not exist is `cerr.KindNotFound` and one the credential cannot see is `cerr.KindUnauthorized`: a TYPED FAILURE, never a value. The returned `Issue` MUST carry an `IssueState.Determined()` state and MUST echo the address it was asked for. See [why issues are on this class](#why-issues-are-on-codeci-and-where-that-stops). |
| `CloseIssue(ctx, fullName, number, reason) error` | Closes that issue, recording `reason`. MUST validate `reason.UsableAsReason()` **before anything else** — `CloseReasonUnspecified` is `cerr.KindInvalid`, and nothing is closed. MUST be **idempotent**: closing an already-closed issue is a success. Recording the reason is best-effort per host (see below). |

`RerunWorkflow`/`CancelWorkflow` are the optional `CIController` operation
behind `CapCIControl` — see [capabilities](#the-codeci-capability-vocabulary)
below.

#### Why `WhoAmI` is required, and not behind a capability

`CIController` is optional because reading and mutating CI are plausibly
different permissions. Identity is not that shape. A host publishes `WhoAmI`'s
answer as its **standing proof** that it authenticates with no token in its own
environment — and an assertion a host can only make for *some* connectors is not
an assertion, it is a fallback path: the per-host duplication this class exists
to remove. There is also no permission for a
capability to gate: a connector that can reach the code host authenticated at
all — which every required operation above needs — can be told which account it
reached it as.

**`Health()` is not a substitute.** It reports reachability and carries no
login, so serving an identity probe from it publishes an empty login beside a
success: the opposite of the claim, wearing a success's shape.

#### Why issues are on `CodeCI`, and where that stops

An issue is a repository object, reached the way everything else in this class
is reached: `<owner>/<name>` plus an id. Two callers need an issue's state, and
its close, **without a board** — "is the issue this work is blocked on still
open" and "close the issue whose work has shipped".

The `Tracker` class cannot express either. `ItemRef.Valid()` requires
`BoardRef.Valid()`, which requires provider **and** instance **and** board, and
refuses an address missing one rather than guessing the rest from context — so
an issue in a repository whose issues are on **no board** has no address there
at all. `GetItems` returns `cerr.KindNotFound` for an item that exists but is
not on the board asked about, and `Apply` takes a `BoardRef`. A board's view of
whether an item is open is a *different fact* from whether the issue is open,
not a smaller one.

⚠️ **The boundary: no issue listing, and no arbitrary issue field writes.** Two
operations, both by explicit address. What that refuses is a second work tracker
growing inside this class — the shape that turned a fifty-operation board update
into an 1,850-request sweep when an abstract backend grew one method per vendor
API. A caller that wants to search issues, or set a label or an assignee, wants
the `Tracker` class.

**Both are required, not capability-gated.** For the read, `WhoAmI`'s argument
transfers: an assertion a host can only make for *some* connectors is not an
assertion, it is a fallback path — and a blocker-resolution path that exists on
only some code hosts leaves every caller keeping a per-host arm. For the close,
this class **already holds write authority on the same repositories**: `MergePR`
is a required write, and a far larger one. `CIController` is gated because
mutating CI is a separately privileged vendor scope; closing an issue is not.

#### `IssueState`, and the vendor limit on close reasons

| Field | Obligation |
|---|---|
| `Issue.State` | MUST be `Determined()` — `IssueStateOpen` or `IssueStateClosed`. ⚠️ It is a three-value vocabulary and **not an `Open bool`** for the reason `Branch.Protected` documents: a bool's zero value asserts one of the two real answers, and here that answer is *closed* — which reads as "your blocker is resolved". `IssueStateUnspecified` asserts neither, so a connector that never looked is visible rather than believed. |
| `Issue.Title` | MAY be empty, like `CheckRun.DetailsURL`. A caller MUST NOT branch on it. |
| `Issue.FullName` / `Issue.Number` | MUST echo what was asked for. A bare state carries no evidence of which issue it is the state of. |

**The close reason is best-effort, per host.** GitHub carries a native issue
close reason; **GitLab's issue close takes no reason at all** (`state_event=close`
and nothing more), so a GitLab connector closes the issue and the distinction is
not durably recorded. That is a vendor limit — the same shape as `DiffFile.Patch`
being allowed to be empty — and it MUST NOT fail the close: the close is the
postcondition the caller needs, and every host supports it. The reason stays
**required from the caller** regardless, because the argument is what makes the
caller decide; a contract that accepted no reason on the hosts that cannot store
one would let callers stop deciding on all of them.

#### `PR.URL` and `Branch.Protected`

| Field | Obligation |
|---|---|
| `PR.URL` | MUST be non-empty. Derivable from a vendor's URL layout, but a contract that leaves it to callers has pushed vendor knowledge back out to every caller. Unlike `CheckRun.DetailsURL`, which a host may genuinely not provide. |
| `Branch.Protected` | MUST be what the code host says, and MUST NOT be left at its zero value because the connector did not look. `false` reads as *"this branch is unprotected"* — the one always-zero field in this class that is actively dangerous. A connector that cannot determine protection state fails `ListBranches` rather than returning branches asserting they are all unprotected. |

**The vendor's own transport split is invisible here.** GitHub answers "what
is the CI status of a ref" over GraphQL and "rerun this workflow" over REST,
because Actions has no rich GraphQL surface for the second. `CodeCI`'s methods
do not know or care which transport an implementation uses for which
operation — "one GraphQL client" (the framework-side half of this Story) means
an implementation must not hand-roll a **second** GraphQL client where a typed
one already exists in its module, not that every operation must share one
wire protocol.

**One token chain.** A `CodeCI` connector receives its credential material
from a `credential.CredentialLoader` at construction, in memory, and never
reads one from the environment — the same discipline `codehost`-shaped
connectors already document, and for the same reason: a fallback to an
ambient variable authenticates as whichever identity that variable happens to
hold, silently, in most environments.

### `Resolution`: reimplemented, not aliased, and here is why

`codeci.Resolution[T]` is the **same shape** as `roster.Resolution[T]` — a
list result in which "the read failed" and "the code host genuinely has none
of these" cannot be confused, the zero value is unresolved rather than empty,
and a truncated (`Partial`) read cannot be built as a readable success. See
[Roster's own writeup](#resolution-an-unresolved-roster-cannot-look-like-a-roster-of-nobody)
for the mechanics — they hold verbatim here, PR/diff/branch/check-run in place
of principal/group/membership.

It is a **second, independent implementation** of that shape rather than a
type alias onto `roster.Resolution[T]`. `roster.Resolution`'s own fault
messages and `String()` rendering name the roster contract by name (`"an
unresolved *roster*..."`, `"[roster of N entries]"`), which would be actively
misleading surfaced from `codeci` — a caller debugging a failed `ListPRs`
reading *"the connector reported success while carrying no list; a *directory*
read that returned nothing..."* would go looking for a directory that was
never involved. `codeci`'s own `Fault`/`FaultError`/`CheckResolution` say
`"the CodeCI contract"` and describe CI-shaped consequences (a truncated
`GetCheckRuns` read as complete masking a failing check) rather than
directory-shaped ones (an offboarding sweep revoking access).

| `Fault` | Meaning | Host-side guard |
|---|---|---|
| `FaultUnresolved` | success carrying no list | `CheckResolution` |
| `FaultPartial` | success asserting `codeci.Partial` — a truncated read reported instead of a typed failure | `CheckResolution` |
| `FaultIncoherentIdentity` | `WhoAmI` reporting success with an identity that is not `Coherent()` — an authenticated identity with no login, or a login named while denying authentication | `CheckIdentity` |

⚠️ **`CheckIdentity` is load-bearing in a way `CheckResolution` is not.**
`Resolution.Items` refuses to read an unresolved resolution whether or not a
host calls the guard; an `Identity` is a plain struct whose fields read fine, so
a host that publishes `WhoAmI`'s answer **without** `CheckIdentity` can publish
an empty login as a success.

### The `CodeCI` capability vocabulary

| Capability | Wire name | Why it exists |
|---|---|---|
| `CapCIControl` | `ci_control` | Reading and mutating CI are plausibly different permissions: a token or app installation scoped for review can read check/pipeline status without holding the separately-privileged, write-scoped permission a rerun or cancel needs — the same reasoning `codehost`'s runner-token capability is built on. Optional operation: `CIController` (`RerunWorkflow`, `CancelWorkflow`). |

A connector without `CapCIControl` MUST still implement every required,
read-only `CodeCI` operation; it simply has nothing behind `CIController`.

### Checking your connector: `codeciconform`

[`codeciconform`](../codeciconform/) runs the parts of this contract a
compiler cannot check:

```go
rep, err := codeciconform.Run(ctx, myConnector, codeciconform.Options{
	Manifest:    myManifest,
	Repo:        "org/populated-repo",   // >=1 open PR, >=1 branch
	UnknownRepo: "org/does-not-exist",
	CheckedRef:  "main",                 // has >=1 check run
	DiffPR:      "1",                    // has >=1 changed file
	UnknownPR:   "999999",
	DraftPR:     "2",                    // IS a draft
	OpenPR:      "3",                    // open, NOT a draft

	ProtectedBranch:   "main",           // the code host DOES protect it
	UnprotectedBranch: "topic",          // it does NOT
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
| `manifest/valid` | the manifest validates and declares this class |
| `class/implements` | `Implements()` reports `connector.ClassCodeCI` |
| `capability/manifest-matches-runtime` | the manifest's `capabilities` and the running connector's `Capabilities()` are the same set |
| `optional/declared-is-implemented` | `ci_control` is declared exactly when `CIController` is implemented — "implemented" is a Go type assertion against the connector's own type, **never** derived from `Capabilities()` |
| `lists/no-empty-success` | `ListPRs`, `ListBranches`, `GetCheckRuns` and `GetDiff` against the fixtures all resolve **readable, with entries** |
| `lists/failure-is-typed-and-fail-closed` | `ListPRs(UnknownRepo)` and `GetDiff(Repo, UnknownPR)` each fail with a classified `cerr.Kind` **and** an unreadable resolution |
| `merge/refuses-unspecified-method` | `MergePR(Repo, OpenPR, MergeMethodUnspecified)` is refused, **without merging** `OpenPR` |
| `merge/refuses-draft` | `MergePR(Repo, DraftPR, MergeMethodMerge)` is refused, **without merging** `DraftPR` |
| `create/refuses-an-incomplete-request` | `CreatePR` with a `CreatePRRequest` carrying no branch, base or title is refused with a classified error, **without opening anything** |
| `branches/protection-is-reported` | `ListBranches` reports `Protected: true` for `ProtectedBranch` **and** `false` for `UnprotectedBranch` — both directions, so neither constant passes |
| `prs/carry-a-url` | every `PR` from `ListPRs(Repo)` carries a non-empty `URL` |
| `identity/answers-with-a-login` | `WhoAmI()` reports an authenticated identity with a non-empty login, or fails with a classified error. An empty login reported as a success is **refused** |
| `health/answers` | `Health()` reports a status or a classified failure |

**The two merge checks and the create check call the mutating operations for
real, against live fixtures, and are safe to run repeatedly.** A conformant connector validates the method and
checks for draft status **before** attempting any merge — which is exactly
what makes calling `MergePR(Repo, OpenPR, MergeMethodUnspecified)` and
`MergePR(Repo, DraftPR, MergeMethodMerge)` non-destructive: neither call ever
reaches the merge itself. The same holds for `create`: a conformant connector
validates the request before it does anything, so the call never reaches the
code host. A connector that has NOT implemented one of these guards merges the
fixture, or opens a pull request with no title — a loud, legible failure of the
check, rather than a silent one.

Every field of `Options` is required, for the same reason `credconform` and
`rosterconform` require theirs: a check that cannot be driven is not skipped,
because a report that is green because nothing looked is worse than no
report.

### Why this class has no Track-B wire protocol yet

`CredentialLoader` and `Roster` each shipped native-only first and gained
their `.proto`/`plugin`/`transport` wiring as a separate, later piece of work.
`CodeCI` is deliberately scoped the same way here: a formal ABC and a
conformance harness that any native Go implementation — GitHub, GitLab, a
third code host — compiles against today. Out-of-process support is a
follow-on Story, not a prerequisite for this one.

## The `Tracker` contract

A `Tracker` is one adapter over **one** work tracker — one board on one instance
of one provider — and its items, their fields, their hierarchy and their
lifecycle. The method-by-method semantics are the package documentation in
[`tracker`](../tracker/tracker.go), which is the contract itself rather than a
description of one; what follows is the shape and the two properties a
re-statement most often drops.

### Five operations, addressed by name

`Tracker` embeds `connector.Connector` and adds exactly five batch-first
operations — `Catalogue`, `Scan`, `GetItems`, `Create`, `Apply` — and
`TestTracker_IsExactlyTheFiveOperations` is the gate that keeps it five. Each
takes a SET, so the per-operation refetching that turns a fifty-operation board
update into a four-figure request sweep is removed by construction, and there is
**one** field-write surface (`Change.Fields`) keyed by field NAME.

No backend identity crosses the boundary: not a field id, not an option id, not
an item id. The `Catalogue` that resolves names is valid for **one call chain**
and MUST NOT be stored, because on a real tracker editing one option of a
single-select field regenerates the identity of *every* option in it — so a
cached id addresses a dead option and writes silently land nowhere.

Every address is fully qualified (`BoardRef`, `ItemRef`): an estate running two
boards on one provider plus a tracker on another cannot route on a board number,
and a parent reference that compared equal across trackers would pass a
cross-tracker link through the refusal that exists to stop it.

`Apply` is **not** transactional and the type says so. `ApplyReport`'s
arithmetic is the contract — every change is either applied or attributed in
`Failed`, and a change listed nowhere was APPLIED — so `CheckApplyReport` takes
the real demand as a separate argument. A report checked only against itself
makes the zero report clean, and a connector that swallowed every change would
render clean.

### Optional operation groups

| Capability | Interface | What it means |
|---|---|---|
| `trains` | `TrainAdmin` | the backend's delivery buckets — milestones, cycles, releases — are administrable through this connector |
| `scoped_trains` | (changes `TrainAdmin`'s contract) | trains are per-`Scope` rather than per-tracker: the same name is a DIFFERENT bucket in each scope |
| `schema_admin` | `SchemaAdmin` | the board's own field schema can be created and extended |

`native_types`, `native_hierarchy`, `cross_scope` and `item_fields` are
behavioural rather than structural: each changes what a write is refused for or
what a `Catalogue` reports. None of the seven is declaration-only.

### Two properties the CONTRACT holds, not each backend

Both are enforced by host-side guards in [`tracker`](../tracker/trains.go), and
both are checked against a running connector by `trackerconform`. Leaving either
to each backend is what makes it a property nobody actually has.

**Union with unknown dominating** — `CheckTrainSets`. The train set is a UNION
over scopes, so the less of it a caller can see the greener a replan looks. A
scope that could not be read comes back with `ScopeTrains.Err` set, and it comes
back: a scope silently dropped from the answer computes the same thing as one
reported empty — "nothing to create here". The guard rejects a missing scope, a
duplicate scope, a scope nobody asked about, an entry carrying `Err` *and* a
list, an unclassified `Err`, and a partition shape that contradicts what the
connector declared with `scoped_trains`.

**Verify by re-reading** — `CheckTrainsCreated`. A create loop that iterated
once still returns successfully for every name it was given, and the count is
the only thing that shows it — so the count is checked against the demand and
then the names are checked against the tracker. A report of
`Requested=8, Applied=8, Failed={}` closes arithmetically and can be wrong about
seven scopes; only the re-read sees that. A re-read that itself fails leaves the
create UNKNOWN rather than done: the guard returns a zeroed report and an error
wrapping the read failure, so the create is neither reported clean (nothing
confirmed it) nor failed (it may well have worked).

### Checking your connector: `trackerconform`

```go
rep, err := trackerconform.Run(ctx, myTracker, trackerconform.Options{ /* fixtures */ })
if err != nil {
	// the run could not be carried out at all
}
if err := rep.Err(); err != nil {
	// the connector ran and is not conformant
}
```

Fifteen named checks. Every write check is driven through a refusal the contract
requires — a change set invalid before any network call, a parent on a foreign
tracker, train specs with the wrong `Scope` polarity — so against a conformant
connector the run writes nothing and is safe to repeat against a live board.
`Tracker.Create` is deliberately not exercised: filing an item cannot be undone
by the harness that filed it.

Every field of `Options` is required, for the same reason every other harness in
this SDK requires its fixtures: an optional fixture is a check that silently
skips, and a check that skipped is reported by nothing. `Run` also refuses a
fixture set that is complete and INCOHERENT — a foreign item that is not
actually on another tracker, an unscannable board that is the scannable one, an
`UnreadableScope` that is one of the readable ones.

Every check has a stub built to violate it, and
`TestRun_EveryCheckHasAViolatingStub` fails if one ever loses its falsifier: a
harness only ever run against compliant input proves nothing about what it would
catch.

## Versioning

This module defines the **Go semantic contract** above: interfaces, value
types, and the error taxonomy. Everything on this page applies identically
whether a connector is compiled into the host (native) or runs out-of-process
(Track-B) — the wire layer below only ever marshals to/from the same
`credential.CredentialLoader`, `roster.Roster`, `ref.Ref`,
`credential.Lease`, `roster.Resolution[T]` and `cerr.Error` types. `codeci.CodeCI`
and `codeci.Resolution[T]` are native-only, as noted above.

### Track-B: the out-of-process wire contract

A Track-B connector is a separate binary — a **provider** — that a host
launches as a subprocess and talks to over gRPC via
[`hashicorp/go-plugin`](https://github.com/hashicorp/go-plugin). The wire
layer composes go-plugin rather than reinventing process/handshake/transport
management; this module adds only the pieces specific to each connector class.

The base-contract messages — `HealthRequest`/`HealthResponse`,
`CapabilitiesRequest`/`CapabilitiesResponse`, `CloseRequest`/`CloseResponse` —
are defined **once**, in
[`proto/connector/v1/connector.proto`](../proto/connector/v1/connector.proto),
and shared by every class's service. `Health` means the same thing for a secret
store and for a directory, and a host reads the same fields off either; one copy
per class would make the number of definitions grow with the class count while
the meaning stayed fixed, and the copies would be free to drift. (buf's
`RPC_REQUEST_RESPONSE_UNIQUE` is excepted for exactly that, with the reason
recorded in `buf.yaml`.)

#### `CredentialLoader` over the wire

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
not change. It also declares and implements a second capability,
`CapBatchResolve`, alongside the baseline `CapRead` — a copier sees a
capability wired correctly end to end, not only the baseline. [`roundtrip_test.go`](../examples/credentialloader-provider/roundtrip_test.go)
in the same directory builds that binary and drives it as a real subprocess
the way a host would — dial, `Dispense`, `Resolve`, `Kill` — confirming the
process actually exits (dies-with-session); [`conform_test.go`](../examples/credentialloader-provider/conform_test.go)
runs `credconform` against `memLoader` in-process and requires both
capabilities to be conformant — the pattern to copy for verifying your own.

#### `Roster` over the wire

| Layer | Package | What it is |
|---|---|---|
| Contract | [`proto/connector/v1/roster.proto`](../proto/connector/v1/roster.proto) | The `.proto` defining `Principal`/`Group`/`Membership`, the three roster-list messages, and the `Roster` gRPC service (`ListPrincipals`, `ListGroups`, `ListMemberships`, `Health`, `Capabilities`, `Close`). Like the `CredentialLoader` proto it carries the presence rules **in the file**, for authors who will never read the Go. |
| Marshalling | [`transport/roster.go`](../transport/roster.go) | `PrincipalToPB`/`FromPB`, `GroupToPB`/`FromPB`, `MembershipToPB`/`FromPB`, and the three `…RosterToPB`/`…RosterFromPB` pairs that own the presence + `empty_by_assertion` rules. Errors share `ErrToStatus`/`ErrFromStatus` with the other class. |
| Transport binding | [`plugin/roster.go`](../plugin/roster.go) | `plugin.RosterName`, `plugin.RosterPluginMap(impl)` for the **provider** side, and `plugin.RosterHostPluginMap(name, manifest, hostVersion)` for the **host** side. `Dispense(plugin.RosterName)` returns a value that itself satisfies `roster.Roster`. |
| Conformance | [`rosterconform.RunOutOfProcess`](../rosterconform/outofprocess.go) | Launches a provider binary, dials it, and runs every check across the real boundary. |

**⚠️ `Resolution[T]` survives the wire, and a naive mapping destroys it.** This is
the part of the Roster wire to get right, and it is the same rule as `Material`'s
rather than a second invention:

| Wire | Meaning |
|---|---|
| no roster message | nothing was resolved |
| roster message with entries | a list (the assertion flag is ignored) |
| roster message, no entries, `empty_by_assertion = true` | a genuinely empty directory |
| roster message, no entries, **no** assertion | **nothing was resolved** |

The last row is the load-bearing one, for the same mechanical reason as the
credential case: **a protobuf `repeated` field defaults to empty, and proto3 does
not put an empty one on the wire at all.** A list mapped straight onto a bare
`repeated` field therefore erases the difference between a failed read and a
directory that genuinely holds nobody — they are the same bytes, and no host can
recover the distinction. The blast radius is worse than the credential case: a
credential resolving to `""` fails one authentication, while a roster reading as
nobody makes a host's offboarding sweep revoke **everyone's** access at once,
correctly according to everything it was told.

That is not a theoretical risk in this repo's history: the naive mapping was
built first and the guard test watched to fail against it, on all four accidental
encodings, before the wrapper message and the flag were introduced. The test is
`TestUnresolvedRosterDoesNotCrossAsAnEmptyDirectory` in [`plugin/roster_test.go`](../plugin/roster_test.go).

**No `partial` on the wire.** A read that stopped early is a typed failure, not a
smaller success, so there is deliberately no encoding for one to arrive in — a
host cannot safely revoke access for a principal the connector never examined.
See [A truncated read cannot look like a complete one either](#a-truncated-read-cannot-look-like-a-complete-one-either).

**The host stub is a guard, not just a transport.** Everything a provider sends
passes through the `roster` package's own host-side checks —
`roster.CheckPrincipals`, `roster.CheckMemberships`, `roster.CheckResolution` —
before a host sees it. So an out-of-process provider cannot make a read that
resolved nothing look like an empty directory, cannot report a machine principal
or an inherited membership it has not declared, and cannot return a membership
for a group the host did not ask about: each comes back as a
`*roster.FaultError` **naming that connector**. The stub reads the provider's
`Capabilities` once, at dial time, because the guards need the declaration and
capabilities are static in this contract; a failed probe refuses the dial rather
than assuming an empty set, which would attribute the host's own missing
question to the provider.

**`min_host_version` is negotiated, not merely declared.**
`manifest.Doc.RequireHost(hostVersion)` is the gate, and
`plugin.RosterHostPluginMap` is the only host-side constructor — it requires both
the manifest and the host's version, so the gate cannot be left off. It **fails
closed**: an absent or unparseable version on either side is refused, because a
host that cannot establish it is new enough must not dial a provider that has
already said it needs more. That is deliberately the reverse of the `cerr`
default (an unclassified failure escalates nothing); the two are not to be
unified — see [`Unknown` does not trip the breaker](#unknown-does-not-trip-the-breaker).

**Reference provider.** [`examples/roster-provider/`](../examples/roster-provider/main.go)
is a complete, vendor-free `Roster` provider: a placeholder directory holding an
active principal, a **suspended** one, a machine identity, nested groups and an
empty group, served via `goplugin.Serve`. Copy `main.go` as the starting point
for a real one (Okta, Entra, Google Workspace, a code host's teams) — swap the
method bodies for calls to the directory API; the `plugin.Handshake` +
`plugin.RosterPluginMap(...)` + `goplugin.Serve` wiring does not change. It
declares and implements two capabilities beyond the baseline
(`transitive_membership`, `machine_principals`) so a copier sees a capability
wired end to end. [`conform_test.go`](../examples/roster-provider/conform_test.go)
runs `rosterconform` in-process; [`roundtrip_test.go`](../examples/roster-provider/roundtrip_test.go)
builds the binary and runs `rosterconform.RunOutOfProcess` against it as a real
subprocess, drives the host-side dial API directly, and confirms the process
actually exits after `Kill` (dies-with-session).

Out of scope here (a separate, later Story): the host-side connector
**registry** — discovery, manifest loading, lifecycle and the broker wiring a
host needs to manage many connectors at once — and a `secrets.Provider`
adapter. What this module ships is each class's wire contract, the host-side
stub and dial helpers a host builds that registry *on*, and a self-contained
reference provider per class.

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
