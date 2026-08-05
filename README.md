# arqtos-sdk-go

`arqtos-sdk-go` is the public Go contract SDK for **arqtos connectors**. It defines
the interfaces, value types, and error taxonomy that a connector implements — and
that a host uses to talk to a connector — without either side depending on the other's
internals.

A connector is a small, focused adapter between arqtos and one external system
(a secret store, a directory, a tracker, ...). This module defines the connector
classes — **`CredentialLoader`**, **`Roster`**, **`CodeCI`** and **`Tracker`** — plus the shared
building blocks every connector class is built from.

This module is dependency-light by design: the semantic contract itself is
stdlib-only. Third-party dependencies are confined to the packages that need
them — `gopkg.in/yaml.v3` for the schemas, the gRPC/go-plugin stack for the
Track-B wire layer, the official MCP Go SDK for the declarative-connector
protocol surface, and `github.com/vektah/gqlparser/v2` for `gqlcheck`.

Those are per-package costs, not per-consumer ones. Under Go's module-graph
pruning, a consumer that imports only `ref` + `cerr` + `credential` compiles
**no** MCP SDK package, records **no** MCP SDK line in its own `go.sum`, and
never fetches the module — measured, not assumed. The SDK is visible to
`go list -m all` as a graph node and nothing more.

## Release versions — what to pin

**The semver tag is the release.** `v0.1.0`, `v0.2.0` — that is what a consumer pins, what the release
notes describe, and the only version this module claims. Pre-1.0, so a minor bump may break.

⚠️ GitHub **milestones on this repo carry `arqtos-cli` train names** (`0.3.48`, `0.3.49`, …). Those are
**planning labels** recording which cli train a piece of SDK work moves alongside — they are **not SDK
versions**, and no consumer should ever pin or cite one. If you read `0.3.48` on an issue here, the
shipped artefact for that work is still whichever `vX.Y.Z` tag it landed in.

Ratified 2026-07-27, after both schemes were found live in one repo.

## Packages

| Package | Purpose |
|---|---|
| [`ref`](ref/) | The `op://<vault>/<item>/<field>` secret-reference type (`Ref`, `Parse`). A `Ref` is a *reference* to a secret — connectors never receive raw credential material as input, only refs. |
| [`cerr`](cerr/) | The connector error taxonomy: a **closed** `Kind` vocabulary (NotFound / Unauthorized / Unavailable / RateLimited / Unsupported / Invalid / Timeout / ContractViolation / Unknown), `Error`, `New`, `KindOf`, `Classified`, `Retryable`, `TripsBreaker`. Callers classify errors by kind, never by string-matching — and `Unknown` deliberately does **not** trip a breaker. |
| [`connector`](connector/) | The base contract every connector implements regardless of class: `Class` (with `Classes()` / `Class.Valid()`, the **closed** class set the manifest's `implements` enum is derived from), `Capability` / `Capabilities`, `Health` / `HealthStatus`, and the `Connector` interface (`Implements`, `Capabilities`, `Health`, `Close`). |
| [`credential`](credential/) | The `CredentialLoader` connector class: `Resolve` / `List` / `Lease` / `Renew` / `Revoke`, plus `Resolution` (a resolve result in which a credential that **did not resolve cannot be read as an empty one** — an empty value is expressible, but only by asserting it with `ResolvedEmpty()`), `Material` (redacted, revealable-on-demand, wipeable secret bytes), `Lease`, and the optional `BatchResolver` operation behind `CapBatchResolve`. |
| [`credconform`](credconform/) | The conformance harness for a `CredentialLoader`: run it in your own CI to check the contract properties a compiler cannot — no empty-success, typed failures, and a manifest whose declared capabilities match the running connector. |
| [`roster`](roster/) | The `Roster` connector class: a **read-only** view of a directory's `Principal`s, `Group`s and `Membership`s, reported as vendor-neutral facts with no arqtos org model in them. Carries `Resolution[T]` (a list result in which a directory that **was not read cannot be read as a directory of nobody** — genuine emptiness is expressible, but only by asserting it with `EmptyRoster()`), the host-side guards `CheckResolution` / `CheckPrincipals` / `CheckMemberships`, and the optional `Watcher` operation behind `CapWatch`. |
| [`rosterconform`](rosterconform/) | The conformance harness for a `Roster`: no unresolved-as-empty, a deactivated principal reported rather than dropped, memberships that match the group requested, typed failures, and declared capabilities that match both the running connector and the data it returns. `RunOutOfProcess` runs all of it against a spawned provider binary, across a real gRPC boundary — the only way the marshalling failures are visible at all. |
| [`codeci`](codeci/) | The `CodeCI` connector class: pull/merge-request lifecycle (`CreatePR` / `ListPRs` / `MergePR` / `GetDiff`), `ListBranches`, `WhoAmI`, CI status (`GetCheckRuns` / `GetWorkflowRun`) and two **board-agnostic issue** operations (`GetIssue` / `CloseIssue`), plus the optional `RerunWorkflow` / `CancelWorkflow` behind `CapCIControl`. `CreatePR` takes a `CreatePRRequest` (so a `draft` can be opened, and the next field added breaks no caller) and MUST validate it before opening anything; `WhoAmI` returns `{Login, Authenticated}` and MUST NOT report an authenticated identity with an empty login — `CheckIdentity` is the host-side guard, because an `Identity` is a plain struct whose fields read fine. Carries `Resolution[T]` (the same fail-closed list shape as `roster.Resolution`, reimplemented here with CI-appropriate wording rather than aliased, so a failed `ListPRs` is never reported as "roster of nobody"), and requires `MergePR` to validate its `MergeMethod` and refuse a draft **before** attempting a merge. The issue pair is addressed as `(fullName, number)` with **no board**, which is what the `Tracker` class cannot express (`ItemRef` requires a `BoardRef`, so an issue on no board has no address there) — `GetIssue` returns an `IssueState` closed vocabulary rather than an `Open bool`, because a bool's zero value asserts *closed* and an unresolvable blocker read as closed tells an operator to start work that is still blocked; an issue that is missing or invisible is a **typed failure, never a state**. `CloseIssue` validates its `CloseReason` before anything else and is **idempotent** — closing an already-closed issue succeeds. ⚠️ Deliberately **no issue listing and no arbitrary field writes**: two operations, both by explicit address, so a second work tracker cannot grow inside this class. Distinct from a code-host-administration class: same vendor, different contract. |
| [`codeciconform`](codeciconform/) | The conformance harness for a `CodeCI`: no empty-success across `ListPRs`/`ListBranches`/`GetCheckRuns`/`GetDiff`, typed fail-closed reads, `MergePR` refusing an unspecified method and a draft and `CreatePR` refusing an incomplete request (each exercised against a real fixture, never merging or opening one), `Branch.Protected` measured in **both** directions so neither constant passes, every `PR` carrying a `URL`, `WhoAmI` refused when it reports an empty login as a success, `GetIssue` measured against **both** an open and a closed fixture so neither constant passes and required to fail — typed — on an issue that cannot be resolved, `CloseIssue` refusing an unspecified reason **before** it resolves the issue (driven at an issue that does not exist, so the ordering is observable and nothing can be closed to discover the guard is missing) and succeeding on an already-closed one, and declared capabilities that match both the manifest and the running connector's actual `CIController` implementation — checked by type assertion, never derived from `Capabilities()` itself. Its own suite asserts that **every** check has a connector built to violate it, and that no such connector breaks a neighbouring check. |
| [`tracker`](tracker/) | The `Tracker` connector class: one work tracker — one board on one instance of one provider — as **five batch-first, name-keyed operations** (`Catalogue` / `Scan` / `GetItems` / `Create` / `Apply`). No backend identity crosses the boundary: every field, option, label, train and item type is addressed by NAME, and the `Catalogue` that resolves names is valid for one call chain and must not be stored — on the tracker this class was designed against, editing one option of a single-select field regenerates the identity of **every** option in it. Every address is fully qualified (`BoardRef`, `ItemRef`), because an estate runs several trackers at once and a board number alone cannot route. `Apply` is **not** transactional and says so: `ApplyReport`'s arithmetic is the contract, and `CheckApplyReport` is the host-side guard that a report accounting for fewer changes than were asked for does not read as a success. Carries `Resolution[T]` **aliased** from `roster` rather than re-implemented, the optional `TrainAdmin` (behind `CapTrains`) and `SchemaAdmin` (behind `CapSchemaAdmin`), and the two `TrainAdmin` guards the contract holds rather than leaving to each backend — `CheckTrainSets` (a scope that could not be read comes back with `Err` set, **never** as a scope with no trains: the set is a union over scopes, so the less of it a caller can see the greener a replan looks) and `CheckTrainsCreated` (a create is verified by **re-reading**, because a create loop that iterated once still returns successfully for every name it was given). |
| [`trackerconform`](trackerconform/) | The conformance harness for a `Tracker`, 15 named checks: the seven every class in this estate carries, plus a scan that pages to exhaustion under both the cheapest and the fullest `Selection`, unknown never reported as empty, the `Selection` echoed back on every item, `Apply` attributing an outcome to every change, a cross-tracker parent refused before any network call, a `Catalogue` re-read rather than held — and the two `TrainAdmin` properties, driven through the same guards a host runs. Every write check is driven through a refusal the contract requires, so against a conformant connector the run is safe to repeat against a live board; every check has a stub built to violate it, and `TestRun_EveryCheckHasAViolatingStub` fails if one ever loses its falsifier. |
| [`githubratelimit`](githubratelimit/) | GitHub's **three** rate-limit mechanisms handled as three separate things: `MechanismPrimary` (the hourly quota, in `x-ratelimit-*` headers), `MechanismSecondary` (abuse detection — a `retry-after`, a 429, or a body naming a secondary limit), and `MechanismGraphQLCost` (a point budget in the response **body**, refused on HTTP **200**). Carries `Classify`, the fail-closed `PrimaryBudget` / `PointBudget` (whose zero value is *unknown*, never *healthy*), and a concurrency-safe `Gate` with `Admit` (pre-emptive, so a multi-step sweep never half-applies), `Do` (which **discards** a value that arrived alongside a rate-limit refusal), jittered backoff and an injected `Clock`. Deliberately vendor-named — see [Handling GitHub rate limits](#handling-github-rate-limits). |
| [`gqlcheck`](gqlcheck/) | Validates the GraphQL a connector **sends** against the schema the backend actually serves — `LoadSchema` / `LoadSchemaFile`, then `ValidateDocument`, `ValidateRootSelection` (a run-time-assembled root selection, wrapped in an operation whose variable declarations are **inferred from the schema** rather than guessed), `ValidateVariables` (the values a document is sent with, which the document itself says nothing about) and `EnumValues`. A `Report` is `Valid` / `Invalid` / `Unknown` / `NotChecked` and **only the first is a pass** — a document that would not parse has not been checked. Every `Finding` carries the rule, the message, the position and the **selection path** that reaches the offending node. Vendor-free: the SDL is the caller's to supply and to pin — see [Validating GraphQL documents](#validating-graphql-documents). |
| [`manifest`](manifest/) | The `connector.yml` schema: `name`, `implements`, `kind`, typed `capabilities`, refs-only `auth`, `min_host_version`. Strict parse, closed enums. |
| [`mcpconform`](mcpconform/) | The MCP protocol surface for **declarative** connectors: checks that an MCP server can be driven by arqtos — including `SessionIndependence`, which POSTs a `tools/list` carrying **no `initialize` and no `Mcp-Session-Id`** and requires a result, so a server that needs a session it will not get is rejected. |
| [`skillspec`](skillspec/) | The `skill.yml` schema (`Skill`, `Parse`, `Validate`) that rides along with the connector SDK. Standalone — not imported by the connector packages. |
| [`scaffold`](scaffold/) / [`cmd/create-arqtos-connector`](cmd/create-arqtos-connector/) | Generates a Track-B `Roster` connector skeleton — see [Scaffolding a new connector](#scaffolding-a-new-connector). |

See [`docs/CONTRACT.md`](docs/CONTRACT.md) for the full method-by-method semantics
and [`docs/SECURITY.md`](docs/SECURITY.md) for the security rules every connector
implementation MUST honour.

## Install

```
go get github.com/arqtiqa/arqtos-sdk-go
```

## Writing a `CredentialLoader` connector

A `CredentialLoader` resolves `ref.Ref` secret references to `credential.Material`,
and optionally supports leased (time-bounded, renewable) material for dynamic
secrets. It embeds the base `connector.Connector` interface, so every
`CredentialLoader` also reports its `Class`, `Capabilities`, `Health`, and
supports `Close`.

Below is a minimal skeleton — a stub that compiles against the real
`credential.CredentialLoader` interface. It returns `cerr.KindUnsupported` for
everything; a real connector replaces each method body with calls to its
backing store.

```go
package myconnector

import (
	"context"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/ref"
)

// Loader is a minimal CredentialLoader skeleton. Swap the bodies for calls
// into your backing secret store.
type Loader struct{}

func (Loader) Implements() connector.Class { return connector.ClassCredentialLoader }

func (Loader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead}
}

func (Loader) Health(ctx context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}

func (Loader) Close() error { return nil }

// Resolve returns a credential.Resolution, not a *Material. A credential
// that did not resolve cannot be read as an empty one: credential.Resolved
// refuses material with no bytes, and the zero Resolution is unreadable. A
// backend that answers a signed-out read with empty output and a success
// exit code therefore surfaces as a failure, with no emptiness check written
// here. A secret that really is stored empty is still expressible — by
// asserting it with credential.ResolvedEmpty().
func (Loader) Resolve(ctx context.Context, r ref.Ref) (credential.Resolution, error) {
	// A real connector: return credential.Resolved(credential.NewMaterial(b))
	return credential.Resolution{}, cerr.New(cerr.KindUnsupported, "Resolve", nil)
}

func (Loader) List(ctx context.Context, scope string) ([]ref.Ref, error) {
	return nil, cerr.New(cerr.KindUnsupported, "List", nil)
}

func (Loader) Lease(ctx context.Context, r ref.Ref) (credential.Resolution, credential.Lease, error) {
	return credential.Resolution{}, credential.Lease{}, cerr.New(cerr.KindUnsupported, "Lease", nil)
}

func (Loader) Renew(ctx context.Context, l credential.Lease) (credential.Lease, error) {
	return credential.Lease{}, cerr.New(cerr.KindUnsupported, "Renew", nil)
}

func (Loader) Revoke(ctx context.Context, l credential.Lease) error {
	return cerr.New(cerr.KindUnsupported, "Revoke", nil)
}

// Compile-time proof Loader satisfies the contract.
var _ credential.CredentialLoader = Loader{}
```

## Writing a `Roster` connector

A `Roster` is a **read-only** adapter over one directory — an identity provider,
a workspace directory, a code host's teams, a flat file. It reports what the
directory says about principals, groups and memberships, and nothing about
arqtos: no org, no team, no role. Mapping directory facts onto arqtos's own
model is the host's job, on the host's side of the boundary.

Three properties do most of the work, and all three exist because of what the
host does with the answer:

- **An unresolved roster is unreadable, not empty.** The list operations return
  `roster.Resolution[T]`, not a slice. `roster.Resolved` refuses an empty list
  and the zero `Resolution` cannot be read, so a directory read that came back
  with nothing — unauthenticated, throttled, misdirected — surfaces as a failure.
  It has to: an offboarding sweep over "the read failed and I returned a zero
  value" deprovisions the whole estate. A directory that genuinely holds nobody
  is still expressible, by asserting it with `roster.EmptyRoster[T]()`.
- **A truncated read is unreadable too, unless you say it isn't.**
  `roster.Resolved(items, completeness)` takes a `roster.Completeness` on
  *every* call — `roster.Complete` or `roster.Partial` — with no default. A
  real directory of any size means `ListPrincipals` paginates internally, and a
  real pagination loop fails partway at least once in production: a 429 on page
  7 of 250. The natural line to write at that point is
  `roster.Resolved(itemsSoFar, roster.Partial)` — and `Resolved` **refuses it**,
  exactly as it refuses an empty list: a page fetched before the failure is not
  a smaller success. Return a typed `cerr` error instead (`cerr.KindUnavailable`,
  `cerr.KindTimeout`, ...), the same way a wholesale read failure already must.
  `roster.Complete` is the ordinary case: an atomic read, or pagination that ran
  to its own natural end.
- **Suspended is not absent.** Report a deactivated identity, with
  `Active: false`. Omitting it tells the host the person left the organisation,
  and the host revokes everything belonging to somebody on parental leave.

```go
package mydirectory

import (
	"context"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// Directory is a minimal Roster skeleton. Swap the bodies for calls into your
// backing directory.
type Directory struct{}

func (Directory) Implements() connector.Class { return connector.ClassRoster }

// Declare only what this directory can actually do. Each capability is a
// measured vendor difference, and rosterconform checks every declaration
// against the running connector AND against the data it returns.
func (Directory) Capabilities() connector.Capabilities {
	return nil
}

func (Directory) Health(ctx context.Context) (connector.Health, error) {
	return connector.Health{Status: connector.Healthy}, nil
}

func (Directory) Close() error { return nil }

// ListPrincipals reports EVERY identity in the directory, including
// deactivated ones (Active: false). Omission means "not in the directory".
//
// If your read paginates internally and a page fails partway through, do NOT
// return roster.Resolved(principalsReadSoFar, roster.Complete) — that is a
// truncated page reported as a whole directory, and an offboarding sweep run
// against it revokes everyone past the failure point. Return a typed cerr
// error instead (cerr.KindUnavailable, cerr.KindTimeout, ...), the same way
// you would for a read that never got a first page.
func (Directory) ListPrincipals(ctx context.Context) (roster.Resolution[roster.Principal], error) {
	// A real connector that read the WHOLE directory: return
	// roster.Resolved(principals, roster.Complete) — never roster.Partial;
	// see the comment above.
	return roster.Resolution[roster.Principal]{}, cerr.New(cerr.KindUnsupported, "ListPrincipals", nil)
}

func (Directory) ListGroups(ctx context.Context) (roster.Resolution[roster.Group], error) {
	return roster.Resolution[roster.Group]{}, cerr.New(cerr.KindUnsupported, "ListGroups", nil)
}

// ListMemberships answers for ONE group. Every returned Membership.GroupID
// must equal groupID, and a group that does not exist is KindNotFound — never
// an empty roster, which a reconcile loop reads as "this group lost everyone".
func (Directory) ListMemberships(ctx context.Context, groupID string) (roster.Resolution[roster.Membership], error) {
	return roster.Resolution[roster.Membership]{}, cerr.New(cerr.KindUnsupported, "ListMemberships", nil)
}

// Compile-time proof Directory satisfies the contract.
var _ roster.Roster = Directory{}
```

Then gate your own CI on [`rosterconform`](rosterconform/):

```go
rep, err := rosterconform.Run(ctx, myRoster, rosterconform.Options{
	Manifest:           myManifest,
	Group:              populatedGroupID,        // must HAVE members
	AbsentGroup:        noSuchGroupID,           // must NOT exist
	SuspendedPrincipal: deactivatedPrincipalID,  // must be deactivated
})
if err != nil {
	return err // the check could not be run at all
}
if err := rep.Err(); err != nil {
	return err // the connector ran, and is not conformant
}
```

Every `Options` field is required, and each fixture has to be the real thing: a
run given an empty group, a group that merely has no members, or no deactivated
principal cannot exercise the check it was meant to drive, and a report that is
green because nothing looked is worse than no report.

**If you ship your roster as an out-of-process provider (`kind: provider`), that
run is not enough.** `Run` takes a `roster.Roster` and cannot tell a native
connector from a host stub talking to a subprocess, so it records
`TransportUnrecorded` rather than claiming the wire was exercised. Add
`rosterconform.RunOutOfProcess`, which launches your built binary, dials it the
way a host does — `min_host_version` negotiation included — and runs every check
across a real gRPC boundary:

```go
rep, err := rosterconform.RunOutOfProcess(ctx, rosterconform.Provider{
	Path:        pathToYourBuiltProviderBinary,
	HostVersion: yourHostContractVersion,
}, opts)
```

An entire class of failure is invisible in-process, because in-process nothing
is marshalled: an unresolved read arriving as an empty directory, a suspended
principal losing its `Active` flag, a membership arriving for a group nobody
asked about. The spawning lives in the SDK rather than in your tests because a
connector repository forbids `os/exec` in its own package tree — see
[`examples/roster-provider/roundtrip_test.go`](examples/roster-provider/roundtrip_test.go)
for the whole pattern.

## Scaffolding a new connector

Rather than copying the skeleton above by hand, [`cmd/create-arqtos-connector`](cmd/create-arqtos-connector/)
generates a complete, buildable **Track-B (out-of-process) `Roster` connector**
project — the shape a third-party connector actually ships, and the one
`rosterconform.RunOutOfProcess` is the gate for:

```
go run ./cmd/create-arqtos-connector \
  -name okta-roster \
  -module github.com/you/okta-roster-connector \
  -out ./okta-roster-connector
cd ./okta-roster-connector
go build ./...
go test ./...
```

The generated project — `go.mod` pinned to this module at `v0.2.0` (the tag
carrying the Roster wire protocol and the out-of-process harness; no
`GOPRIVATE` or credential setup, because this module is public), `main.go`,
`connector.yml`, and an in-process conformance test — **compiles and passes
rosterconform immediately**, against a fixed placeholder directory, before a
single line of real logic exists. `main.go`'s comments carry the two mistakes
this contract has already made someone pay for once each: an unresolved read
must never arrive as an empty directory, and `Capabilities()` must be
hardcoded honest rather than derived from `connector.yml`. It also does not,
and cannot, declare `watch` — see [`plugin/roster.go`](plugin/roster.go).

## Verifying a connector against the contract

Run [`credconform`](credconform/) against your own connector, in your own CI,
before arqtos ever loads it:

```go
rep, err := credconform.Run(ctx, myLoader, credconform.Options{
	Manifest:     myManifest,                    // what you publish
	Resolvable:   []ref.Ref{presentRef},         // must resolve
	Unresolvable: absentRef,                     // must fail, and typed
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
| `manifest/valid` | the manifest validates and declares only capabilities this class defines |
| `capability/manifest-matches-runtime` | the manifest and `Capabilities()` declare the same set |
| `batch/declared-is-implemented` | `batch_resolve` is declared exactly when `credential.BatchResolver` is implemented |
| `resolve/no-empty-success` | a resolvable reference comes back carrying material — not a success carrying nothing, and not a `ResolvedEmpty()` assertion either |
| `failure/typed` | an unresolvable reference fails with a classified `cerr.Kind`, not vendor prose |
| `batch/results-match-request` | batch results correspond one-for-one, in order, with the references requested |

Every one of those checks has a test that drives it with a connector built to
violate exactly the property it checks — a resolve that returns empty with no
error, a failure carrying only the backend's own wording, a manifest declaring
a batch operation that is not there. A harness only ever run against compliant
input proves nothing about what it would catch.

The remaining conformance surface — full contract shape, secret-handling (no
material to logs, disk or wire; dies-with-session), and protocol-version
negotiation — lands alongside these checks.

## Validating GraphQL documents

A connector that talks GraphQL sends text. A fixture server answers whatever it
is asked, so a suite built on one is **green for a document the real backend
rejects outright** — measured: a leaf added to an inline fragment on an interface
that has no such field, every read through that document broken in production,
and not one test red.

[`gqlcheck`](gqlcheck/) is the check a fixture server cannot stand in for. It asks
document-versus-**schema**, which is a different question from
document-versus-decode (does the selection request every leaf my struct carries?)
and neither implies the other.

```go
schema, err := gqlcheck.LoadSchemaFile("testdata/schema.graphql") // YOUR pinned SDL
if err != nil {
	return err
}

r := schema.ValidateDocument("catalogueQuery", catalogueQuery)
if !r.OK() {
	t.Fatalf("%s", r) // names the rule, the message and the selection path
}
```

Three things about it are load-bearing:

- **Only `Valid` is a pass.** `Unknown` is what a document that would not parse
  gets, and `NotChecked` is what a caller records when it did not look at all.
  `Report.OK()` is false for both — a checker that reports the unchecked as clean
  is how the defect it exists for ships behind a green suite.
- **This module vendors no schema.** A vendored SDL is a vendor artefact with a
  refresh policy attached, and it belongs with whoever depends on that backend.
  ⚠️ Where two modules check against the same backend, they must read **one**
  pinned copy: two pins drift, and the second one drifts silently.
- **It does not find your documents.** Something has to hand it the text. Driving
  every document a package contains — a Go-source sweep, a wire gate that walks
  back from the transport — is caller-side machinery bound to a package loader,
  and it is deliberately not in this module. `gqlcheck` takes a schema and text
  and returns a verdict; see the package doc for the three corrections recorded
  against that machinery, kept there for anyone building the finding half.

## Handling GitHub rate limits

`githubratelimit` is the one package here named after a vendor, and that is
deliberate. Every other package is vendor-neutral because an ABC derived from a
single backend encodes that backend's accidents as contract. These three
mechanisms **are** GitHub's, down to the header names and the units, so the name
says so — nobody should reach for this as "the rate limiter" and quietly inherit
GitHub's shape for a backend that does not have it.

It lives in the SDK because a host reaches GitHub through more than one surface
(a tracker and a PR/CI surface at least) and each needs the identical
discipline. Two implementations are two implementations that drift, and the
drift is invisible until one of them under-waits in production.

### The three mechanisms are not one mechanism

| | Signal | Response |
|---|---|---|
| **Primary** — the hourly quota | `x-ratelimit-remaining` / `x-ratelimit-reset`, on **every** response including successful ones | wait until the reset; and because the budget arrives before exhaustion, `Gate.Admit` waits **before** spending it |
| **Secondary** — abuse detection (burst rate, concurrency) | a `retry-after` header, a **429**, or a 403 whose body names a secondary limit | honour `retry-after`; **jittered** exponential backoff when it names none. Waiting out the *primary* reset does not clear this |
| **GraphQL point cost** | `data.rateLimit` in the response **body** — no header at all — and refused on HTTP **200** with a `RATE_LIMITED` entry in `errors` | wait until `resetAt`; accounted separately, because one query can cost hundreds of points while the REST request count barely moves |

On a secondary refusal the primary budget typically reads **healthy**, so a
handler that derived its wait from the budget computes a wait of *nothing* and
retries straight back into the limit. That is the failure this package exists to
prevent, and `Classify` reports which mechanism refused so the two never
collapse.

### Fail closed, in two places

`Do` returns the zero value alongside any rate-limit error — even when the
attempt reported no error. GitHub refuses a point-exhausted GraphQL query with
HTTP 200 and *partial* `data`, so a caller decoding that body has a value, a nil
error, and half an answer; a truncated result indistinguishable from a complete
one is exactly the defect being discarded here.

`PrimaryBudget` and `PointBudget` have an **unknown** zero value, not a healthy
one. An unmeasured budget is never `Exhausted()` (no evidence of exhaustion, so
nothing to wait for) and has no `Headroom()` (no evidence of room, so nothing to
admit a sequence against).

### A multi-step sequence completes, or does not start

The guarantee is **completes-after-waiting**, and it is bought by not letting the
sequence begin until the whole of it fits:

```go
gate := githubratelimit.New(githubratelimit.Options{
    Reserve: 50, // keep room for the final status read and the error report
    Notify: func(w githubratelimit.Wait) {
        log.Printf("waiting %s on the github %s limit until %s (attempt %d/%d)",
            w.Delay, w.Mechanism, w.Until.Format(time.RFC3339), w.Attempt, w.Attempts)
    },
})

// One call, before the first mutation, for the whole sequence.
if err := gate.Admit(ctx, len(mutations)); err != nil {
    return err // typed, and it names when the limit clears
}
for _, m := range mutations {
    apply(m)
}
```

A sequence larger than the entire quota is refused as `cerr.KindInvalid` without
waiting: no amount of waiting makes it fit, and a wait that can never succeed is
a hang with extra steps. The gate cannot roll back a mutation that already
happened — admitting the whole sequence up front is the mechanism that keeps
there from being one.

### One request

```go
pr, err := githubratelimit.Do(ctx, gate, "GetPR",
    func(ctx context.Context) (PR, githubratelimit.Observation, error) {
        resp, err := client.Do(req.WithContext(ctx))
        if err != nil {
            return PR{}, githubratelimit.Observation{}, err
        }
        defer resp.Body.Close()
        body, err := io.ReadAll(resp.Body)
        if err != nil {
            return PR{}, githubratelimit.FromHTTP(resp, nil), err
        }
        return decode(body), githubratelimit.FromHTTP(resp, body), nil
    })
```

`Do` retries **rate-limit refusals only**. A 500, a 404, a 403 for a missing
scope, a transport error — each is returned verbatim on the first attempt. A
wrapper that also retried those would turn a broken backend into a slow one and
a missing permission into a five-attempt stall.

### Waits are visible, and time is injected

`Options.Notify` receives a `Wait` **before** each pause, carrying the
mechanism, the delay, the wall-clock time it ends, and whether the mechanism
*dictated* the wait or the gate computed a jittered backoff. A sweep that pauses
for eleven minutes in silence is indistinguishable from a hung one, and an
operator kills the second — turning a wait that would have completed into the
half-applied sweep the wait existed to prevent.

`Options.Clock` is the only source of now and of sleeping, so tests for a
package whose whole job is waiting do not wait: they assert the **computed**
backoff and the **number** of attempts, both exact. The package's own test suite
is gated against reading the real clock at all — a test asserting elapsed time
is asserting a property of the CI runner, and under `-race` with coverage
instrumentation that runner is slow enough to make such a test flake rather than
fail honestly.

`Options.Resource` scopes a gate to **one** quota bucket. Search is 30 requests
a minute against core's 5000 an hour, so a gate that folded them into one number
would stall every core request for the rest of the hour the first time a search
was refused. A host reaching more than one bucket builds more than one gate.

### Three sharp edges the API deliberately encodes

**The header constants are canonical, not as GitHub documents them.**
`githubratelimit.HeaderRemaining` is `"X-Ratelimit-Remaining"` — lower-case `l`.
`http.Header` is a map whose keys `Get`/`Set` canonicalize, so the documented
spelling `"X-RateLimit-Remaining"` is **invisible** to `Get` when a consumer
builds or reads the map directly. Use the constants and both routes work.

**A `retry-after` is clamped at `MaxRetryAfter` (24h).** `time.Duration` counts
nanoseconds in an `int64`, so `retry-after: 18446744074` multiplied out
*overflows* and comes back as a plausible-looking 290 ms — a silent under-wait
dressed as compliance. Nothing longer than a day is a rate limit GitHub has, so
the value is clamped down rather than rejected: the mechanism is still reported,
and only the duration is capped.

**A backoff is never zero.** `Options.Jitter` of 1 (full jitter) has a window
floor of zero by definition, so an unlucky draw would compute no delay at all —
a tight retry straight back into the limit that just refused the request.
`Gate.Backoff` floors at `MinBackoff`.

A zero `Gate` — `var g Gate`, or one embedded in another struct — defaults itself
on first use rather than panicking on its nil clock or reporting a rate limit for
a request it never made.

## Declarative connectors and MCP

A **declarative** connector ships no Go code: it points arqtos at an MCP
server and maps connector operations onto that server's tools. Its contract is
the MCP protocol, and arqtos tracks that protocol through the **official MCP
Go SDK** (`github.com/modelcontextprotocol/go-sdk`) — pinned here at the same
version the host uses. There is deliberately **no arqtos dual-protocol shim**:
compatibility across protocol revisions comes from the SDK's wrapper.

Two things bind such a connector, both checkable before arqtos ever dials it:

- **Do not depend on a session.** arqtos does not promise a long-lived
  connection, and the specification is moving to a stateless core.
- **Statelessness is configured, not inferred.** Serving over streamable HTTP
  with the Go SDK requires setting `Stateless: true` explicitly; the default
  handler rejects a client that skipped the handshake.

[`mcpconform`](mcpconform/) turns those into a check you can run in your own
CI:

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

The check that decides it is `session-independent`: a raw `tools/list` POST with
**no `initialize` and no `Mcp-Session-Id`**, which must come back with a result.
It has to speak the wire rather than use the SDK's Go client, because that
client always performs the handshake — every connection it opens is a session,
so a check built on it can only compare sessions to each other. It is exported
as `mcpconform.SessionIndependence` for pointing at a deployed endpoint on its
own:

```go
if res := mcpconform.SessionIndependence(ctx, endpoint, nil); !res.Pass {
	return fmt.Errorf("%s: %s", res.Name, res.Detail)
}
```

A server built with `mcp.StreamableHTTPOptions{Stateless: false}` — the SDK's
default — **fails** this check, which is the point.

See [`docs/CONTRACT.md`](docs/CONTRACT.md#declarative-connectors-the-mcp-protocol-surface)
for the full check list and the protocol-version compatibility notes.

## Versioning and the wire protocol

This module is the **Go semantic contract**: interfaces, types, and error
taxonomy. That contract applies identically whether a `CredentialLoader` is
compiled into the host (native) or runs out-of-process as a separate
provider binary (Track-B), talked to over gRPC via
[`hashicorp/go-plugin`](https://github.com/hashicorp/go-plugin).

This module also ships the Track-B wire layer built on top of that same
contract, for **both** connector classes: the `.proto`/generated stubs
(`proto/`, `connectorpb/`), the marshalling and error-mapping helpers
(`transport/`), the go-plugin handshake and dispense wiring (`plugin/`), and
the provider manifest schema (`manifest/`) — whose `min_host_version` is
negotiated at dial time, not merely declared. See
[`docs/CONTRACT.md`](docs/CONTRACT.md#track-b-the-out-of-process-wire-contract)
for the full layer-by-layer breakdown, and
[`examples/credentialloader-provider/`](examples/credentialloader-provider/main.go)
or [`examples/roster-provider/`](examples/roster-provider/main.go) for a
complete, vendor-free reference provider to copy as a starting point for a
real one.

The one rule a provider author must not get wrong is the same for both classes:
a read that resolved **nothing** must not arrive at the host looking like an
empty secret or an empty directory. Emptiness is *asserted* on the wire, never
inferred from an absent field — because a protobuf `repeated` field and a
zero-length `bytes` field are both simply absent, so the encoding a hurried
author emits by accident has to mean "unresolved". For a roster the cost of
getting it wrong is an estate-wide deprovision.

Out of scope here (a separate, later contract): the host-side connector
registry — discovery, lifecycle and the broker wiring for managing many
connectors at once — and a `secrets.Provider` adapter.

## CI: the private-content firewall

Every pull request and every push to `main` scans the tracked tree against
[`.github/scripts/private-content-denylist.txt`](.github/scripts/private-content-denylist.txt).
A match **fails the build**; a missing denylist exits **2 (misconfigured)** rather
than reporting a clean scan.

⚠️ **This repository is public, so it is the one where the scan matters most** —
and it was the one running without it (arqtos-sdk-go#38). A repo with no gate
is not "ungated pending work": the green check is already there and nothing is
producing it.

⚠️ **The denylist here omits one rule its siblings carry: the `op://` locator
shape.** The [`ref`](ref/) package exists to parse those references, so its tests
contain them by necessity — and an `op://vault/item/field` reference is an
*address*, carrying no credential material. Credential-material rules are
unaffected: a real token planted in `ref/ref_test.go` is still refused, by name.
The omission is argued in full in the denylist's own header, along with why it
must not be widened.

## License

Apache-2.0. See [`LICENSE`](LICENSE).
