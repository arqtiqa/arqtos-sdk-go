# Security rules for `CredentialLoader` connectors

A `CredentialLoader` connector sits directly on the secret-handling path: it is
the thing that turns a reference into live credential material. The rules
below are not stylistic preferences — they are the invariants a connector
implementation MUST honour to be safe to run against a real backing store.
They apply to every implementation, native (in-process) or out-of-process,
first-party or third-party.

## Refs-only in

A `CredentialLoader` method never accepts raw credential material as an
argument **when identifying which secret to resolve**. Every input that
identifies "which secret" is a
[`ref.Ref`](../ref/ref.go) — an `op://<vault>/<item>/<field>` reference — never the
secret value itself. This is enforced structurally: `Resolve` / `List` /
`Lease` take a `ref.Ref` (or a `Lease`, which is a handle, not material).

**`BindAuth` is the one inbound material path**, and it is not a "which secret"
argument. The host supplies the connector's own outward-auth bootstrap — a
token, or `client_id` + `client_secret`, same API — over the authenticated
provider channel. That set MUST NOT arrive via argv, an inherited environment,
a log line, or a persisted payload. `credential.CheckBootstrap` refuses a
missing or duplicate key by **field name** and never embeds material.
`Bootstrap.String()` / `GoString()` redact; `Zero()` wipes.

If you find yourself wanting to pass a secret value *into* `Resolve`, that is a
sign the design is wrong — plumb a `ref.Ref` through instead and let the
connector resolve it. Do not "fix" a missing bootstrap key by reading `os.Getenv`.

## `Material` redacted + wiped

Resolved secret material is always wrapped in
[`credential.Material`](../credential/credential.go) — and returned from
`Resolve`/`Lease` inside a
[`credential.Resolution`](CONTRACT.md#resolution-an-unresolved-credential-cannot-look-like-an-empty-one)
— never passed around as a bare `[]byte` or `string`:

- `Material.String()` / `Material.GoString()` are defined on the **value**
  receiver, so both a `Material` value and a `*Material` pointer — including a
  `Material` embedded by value in another struct — satisfy `fmt.Stringer` /
  `fmt.GoStringer` and always render the fixed redacted placeholder. This
  means `Material` (value or pointer) is safe to pass to `fmt.Sprintf("%v", …)`,
  a logger, `%+v` in a panic trace, `%#v`, or any other `%v`/`%+v`/`%#v`/`%s`-style
  formatting without leaking the secret — accidental logging of a `Material`
  cannot print the secret. The backing `b []byte` field is unexported, so
  there is no field-access path around this either.
- The raw bytes are reachable only through the explicit `Reveal()` call. Any
  code path that needs the actual secret value must call `Reveal()`
  deliberately and hold the result for the shortest possible scope.
- `Resolution.String()` / `GoString()` redact the same way, on the value
  receiver, so a `Resolution` is as safe to log as the `Material` it carries.
  They do distinguish an unresolved resolution from a resolved one, which is
  diagnosis rather than material.
- `Material.Zero()` wipes the backing bytes in place. Callers MUST call
  `Zero()` on every `*Material` once it is no longer needed — this is the
  dies-with-session guarantee: material does not outlive the operation (or
  session) that requested it. A connector implementation must not retain a
  `*Material` it has handed out, and must not cache resolved material past the
  lifetime the host expects (see `Close()` below).

## Passthrough prohibited

A connector is not a proxy. It resolves references *for the host*, on
requests the host originates — it does not accept and forward arbitrary
requests from a third party, and it does not expose a general secret-fetch
surface to anything other than the host process that owns it. A connector
that finds itself forwarding an unresolved ref, or resolving on behalf of a
caller it cannot attribute to the host session, is out of contract.

## Dies-with-session

Nothing a `CredentialLoader` resolves outlives the session that requested it:

- Leases obtained via `Lease(...)` MUST be revoked (`Revoke(...)`) when the
  session that requested them ends, not left to expire naturally on their
  `TTL`.
- `Close()` on the connector itself must not leave resolved `Material` or
  active leases behind — any in-memory material still held must be wiped, and
  any leases the connector is tracking on the caller's behalf should be
  revoked or handed back for host-side revocation, per the connector's
  documented `Close()` semantics.
- A connector implementation MUST NOT persist resolved material to disk, an
  environment variable, or any store outside the process's own transient
  memory.

## Host-side PERMAFROST audit of every action

Every `CredentialLoader` action — `Resolve`, `List`, `Lease`, `Renew`,
`Revoke`, and `BindAuth` when declared — is audited on the host side
(PERMAFROST), independent of whether the underlying backing store keeps its
own access log. A connector does not need to implement its own audit trail to
satisfy this contract, but it MUST NOT do anything that would prevent the
host from attributing an action to the request that caused it — e.g. it must
not batch, cache, or reorder calls in a way that decouples a resolve from the
request that triggered it.

## Secret material on the connector/host boundary

Two paths carry secret bytes, and only those two:

1. **Outbound** — the `*Material` returned from a `Resolve` or `Lease` the
   host itself made, for the exact `ref.Ref` (or `Lease`) it asked about.
2. **Inbound bootstrap** — `BindAuth`, the host supplying the connector's own
   outward-auth key set (see [Refs-only in](#refs-only-in)). Those bytes are
   not a resolved secret and MUST NOT be logged, persisted, or copied into
   argv or the process environment.

A connector must not:

- return material for a ref other than the one requested,
- return material the host did not ask for (e.g. bundling adjacent secrets
  "for efficiency"),
- expose any side channel (logs, metrics, error messages) that carries secret
  bytes. In particular, `cerr.Error`'s `Err` field and `Error()` string MUST
  NOT embed resolved material — wrap the underlying store error's message,
  not the secret value.

## Track-B: error strings cross the wire verbatim

Over the Track-B gRPC transport, outbound `Material` and inbound `BindAuth`
bootstrap entries are treated as sensitive: they cross as the
redacted-on-format `credential.Material` / `credential.Bootstrap` types,
never logged or serialized as a bare string en route.

A provider's **error messages do not get that treatment**. `transport.ErrToStatus`
preserves `err.Error()` verbatim into the gRPC status message it sends back
to the host, and `transport.ErrFromStatus` reconstructs the error from that
same status message on the way in — the string makes a full round trip
unredacted, and a host is free to log a received error (this is exactly what
the PERMAFROST audit trail above does for every action). Consequently:

- A connector implementation MUST NOT embed secret material — a resolved
  value, a raw credential, a token, a password — in any `error` it returns
  from a `CredentialLoader` method, including errors wrapped via `cerr.New`.
  This applies identically whether the connector runs natively or as a
  Track-B provider; Track-B just makes the leak also cross a process
  boundary and land in a gRPC status the host may persist.
- If the backing store's own error includes secret-shaped material (some
  APIs echo the failing credential back in an error body), the connector
  MUST strip or redact it before wrapping, not pass it through.
- See [`docs/CONTRACT.md`](CONTRACT.md#track-b-the-out-of-process-wire-contract)
  for how `transport.ErrToStatus`/`ErrFromStatus` map every `cerr.Kind` to a
  gRPC code and carry the message across.
- `cerr.RateLimited` attaches a non-secret `Observation` (opaque bucket,
  observed-at, retry/reset provenance, upstream count kind). `Error()` with
  `Quota` set does **not** echo the wrapped cause. Persist only those fields
  as cooldown metadata — never a credential, URI or secret-value hash. An old
  peer without the detail still classifies `RESOURCE_EXHAUSTED` as
  `KindRateLimited` with `Quota == nil`.

## Placeholders in examples and docs

Any example, test fixture, or doc snippet that needs to show a secret
reference uses an `op://<vault>/<item>/<field>`-shaped placeholder — never a real
vault, item, or field name from a live 1Password/Infisical/Vault instance, and
never a real secret value. See [`ref.Parse`](../ref/ref.go) for the reference
shape this SDK expects.
