# arqtos-sdk-go

`arqtos-sdk-go` is the public Go contract SDK for **arqtos connectors**. It defines
the interfaces, value types, and error taxonomy that a connector implements — and
that a host uses to talk to a connector — without either side depending on the other's
internals.

A connector is a small, focused adapter between arqtos and one external system
(a secret store, a record store, a tracker, ...). This module defines the first
connector class, **`CredentialLoader`**, plus the shared building blocks every
connector class is built from.

This module is dependency-light by design: the contract itself is stdlib-only; the
`skillspec` package (a schema that rides along in this repo) is the only package
that pulls in a third-party dependency (`gopkg.in/yaml.v3`).

## Packages

| Package | Purpose |
|---|---|
| [`ref`](ref/) | The `op://vault/item/field` secret-reference type (`Ref`, `Parse`). A `Ref` is a *reference* to a secret — connectors never receive raw credential material as input, only refs. |
| [`cerr`](cerr/) | The connector error taxonomy: `Kind` (NotFound / Unauthorized / Unavailable / Unsupported / Invalid / Timeout / Unknown), `Error`, `New`, `KindOf`, `Retryable`. Callers classify errors by kind, never by string-matching. |
| [`connector`](connector/) | The base contract every connector implements regardless of class: `Class`, `Capability` / `Capabilities`, `Health` / `HealthStatus`, and the `Connector` interface (`Implements`, `Capabilities`, `Health`, `Close`). |
| [`credential`](credential/) | The `CredentialLoader` connector class: `Resolve` / `List` / `Lease` / `Renew` / `Revoke`, plus `Material` (redacted, revealable-on-demand, wipeable secret bytes) and `Lease`. |
| [`skillspec`](skillspec/) | The `skill.yml` schema (`Skill`, `Parse`, `Validate`) that rides along with the connector SDK. Standalone — not imported by the connector packages. |

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

func (Loader) Resolve(ctx context.Context, r ref.Ref) (*credential.Material, error) {
	return nil, cerr.New(cerr.KindUnsupported, "Resolve", nil)
}

func (Loader) List(ctx context.Context, scope string) ([]ref.Ref, error) {
	return nil, cerr.New(cerr.KindUnsupported, "List", nil)
}

func (Loader) Lease(ctx context.Context, r ref.Ref) (*credential.Material, credential.Lease, error) {
	return nil, credential.Lease{}, cerr.New(cerr.KindUnsupported, "Lease", nil)
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

## Scaffolding a new connector

Rather than copying the skeleton above by hand, a scaffolder generates a working
connector project (module, stub implementation, tests) from a template — see
`create-arqtos-connector`. Use it to start a new `CredentialLoader` connector
project with the compile-time contract check already wired in.

## Verifying a connector against the contract

A connector implementation should be run against the conformance harness, which
exercises the `CredentialLoader` semantics described in
[`docs/CONTRACT.md`](docs/CONTRACT.md) — capability-gated behavior, error-kind
correctness, lease/renew/revoke lifecycle, and the `Material` redaction/wipe
invariants — independent of which backing store the connector talks to.

## Versioning and the wire protocol

This module is the **Go semantic contract**: interfaces, types, and error
taxonomy. That contract applies identically whether a `CredentialLoader` is
compiled into the host (native) or runs out-of-process as a separate
provider binary (Track-B), talked to over gRPC via
[`hashicorp/go-plugin`](https://github.com/hashicorp/go-plugin).

This module also ships the Track-B wire layer built on top of that same
contract: the `.proto`/generated stubs (`proto/`, `connectorpb/`), the
marshalling and error-mapping helpers (`transport/`), the go-plugin
handshake and dispense wiring (`plugin/`), and the provider manifest schema
(`manifest/`). See
[`docs/CONTRACT.md`](docs/CONTRACT.md#track-b-the-out-of-process-wire-contract)
for the full layer-by-layer breakdown, and
[`examples/credentialloader-provider/`](examples/credentialloader-provider/main.go)
for a complete, vendor-free reference provider to copy as a starting point
for a real one. Out of scope here (a separate, later contract): the
host-side registry and dial/broker wiring, and a `secrets.Provider` adapter.

## License

Apache-2.0. See [`LICENSE`](LICENSE).
