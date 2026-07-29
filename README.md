# arqtos-sdk-go

`arqtos-sdk-go` is the public Go contract SDK for **arqtos connectors**. It defines
the interfaces, value types, and error taxonomy that a connector implements — and
that a host uses to talk to a connector — without either side depending on the other's
internals.

A connector is a small, focused adapter between arqtos and one external system
(a secret store, a directory, a tracker, ...). This module defines the connector
classes — **`CredentialLoader`**, **`Roster`** and **`CodeCI`** — plus the shared
building blocks every connector class is built from.

This module is dependency-light by design: the semantic contract itself is
stdlib-only. Third-party dependencies are confined to the packages that need
them — `gopkg.in/yaml.v3` for the schemas, the gRPC/go-plugin stack for the
Track-B wire layer, and the official MCP Go SDK for the declarative-connector
protocol surface.

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
| [`codeci`](codeci/) | The `CodeCI` connector class: pull/merge-request lifecycle (`CreatePR` / `ListPRs` / `MergePR` / `GetDiff`), `ListBranches`, and CI status (`GetCheckRuns` / `GetWorkflowRun`), plus the optional `RerunWorkflow` / `CancelWorkflow` behind `CapCIControl`. Carries `Resolution[T]` (the same fail-closed list shape as `roster.Resolution`, reimplemented here with CI-appropriate wording rather than aliased, so a failed `ListPRs` is never reported as "roster of nobody"), and requires `MergePR` to validate its `MergeMethod` and refuse a draft **before** attempting a merge. Distinct from a code-host-administration class: same vendor, different contract. |
| [`codeciconform`](codeciconform/) | The conformance harness for a `CodeCI`: no empty-success across `ListPRs`/`ListBranches`/`GetCheckRuns`/`GetDiff`, typed fail-closed reads, `MergePR` refusing an unspecified method and a draft (each exercised against a real fixture, never merging it), and declared capabilities that match both the manifest and the running connector's actual `CIController` implementation — checked by type assertion, never derived from `Capabilities()` itself. |
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

## License

Apache-2.0. See [`LICENSE`](LICENSE).
