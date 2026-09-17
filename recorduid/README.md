# Record UID declaration and codec v1

`recorduid` is the shared offline syntax boundary for human record citations. It has no runtime consumer in this SDK: Core's records callers integrate it separately. It does not mint UUIDs, register sources, allocate ordinals, accept reservations, authenticate history, resolve records or grant permissions. UUIDv7 remains the permanent machine identity. Design: dcn-arq-00031, record architecture §3.1, record contracts §1.1, config detail §6d.

## Typed contract

`Declaration` has `schema: arqtos-uid-declaration/v1`, stable `org_id`, stable `sequence_id`, exact caller-pinned `revision`, declared `kind`, and `mode`. The org identity scopes comparisons; an org slug is not global identity. Revision and identity fields are nonempty opaque UTF-8 strings without whitespace or control characters. Their shape does not authenticate them. The caller selects and verifies the actual registration/revision, project registration or repository identity before using this syntax metadata.

| Mode | Required metadata | Permitted operation |
|---|---|---|
| `issuing` | `namespace`; no `historical_prefix` | Validate, propose canonical rendering, parse canonical labels |
| `historical` | Exact literal `historical_prefix`; no issuing namespace | Validate and parse only; Render always refuses |

An issuing `Namespace` contains a `profile` and declared `kind_prefix`. For `simple`, `project_ref` names the explicitly registered issuing project/workspace, and `project_slug` freezes its resolved slug. Full coordinates must be absent. For `full`, `org_slug`, stable org-unique `host_key` and provider-native `repository_id` are required; project coordinates must be absent. The native ID is a string, including numeric-looking IDs, with the same UTF-8/no-whitespace/no-control restriction. It has no integer-size ceiling and is never case-folded, Unicode-normalized, trimmed or converted to a number. This package cannot prove host-key nonreuse, repository observation or project registration.

`kind`, `kind_prefix`, `project_slug`, `org_slug` and `host_key` use `[a-z][a-z0-9]*(-[a-z0-9]+)*`. Kinds/prefixes are supplied data, not a closed seed-kind enum. A docs mapping into another workspace or an external docs corpus is not another kind or issuer.

The Go types carry JSON and YAML field tags. They are not file loaders: a consuming boundary must strictly decode its own complete input, reject unknown/duplicate fields and type coercions, and then call `Validate`. Ordinary `json.Unmarshal` alone is not declaration validation. Do not put native IDs through generic floating-point JSON values. [Golden typed-input vectors](testdata/declarations.v1.json) carry ordinals as decimal strings for transport; the Go Render/Parse API uses `uint64`.

## Canonical new rendering

The unencoded semantic profiles remain:

```text
simple: kind-prefix / issuing-project-slug / ordinal
full:   kind-prefix / org-slug / host-key / native-repository-id / ordinal
```

Encode each non-ordinal component independently, then join with raw `-`. Only ASCII `a-z`, `0-9`, `_`, `.` and `~` pass unchanged. Encode every other UTF-8 byte as `%hh` with exactly two lowercase hexadecimal digits. In particular, hyphen is `%2d`, percent is `%25`, and uppercase `A` is `%41`. No delimiter can enter a component and no alternate percent spelling is canonical. The declaration retains the exact unencoded value. Parsing compares the label to the supplied declaration's encoded prefix; it does not recover authority or location from components.

Examples: ordinary coordinates yield `doc-example-00001` and `doc-ex-host-0042-00001`. A custom `field-note` kind prefix renders as `field%2dnote`. Native ID `repo-A/%2d:é` renders as `repo%2d%41%2f%252d%3a%c3%a9`. Literal escape-looking IDs and Unicode normalization variants remain distinct. Raw hyphen concatenation, uppercase escape hex, unnecessary escaping and mismatched namespace metadata are refused for new labels.

Ordinals are positive integers from 1 through 18446744073709551615. Render pads to a minimum of five digits; 99999 grows to 100000 without truncation. Parse rejects zero, signs, exponents, decimal points, overflow, fewer than five digits and padding beyond the canonical minimum. There is no float conversion. A caller incrementing the maximum must detect exhaustion before calling Render; this is not an allocator.

## Explicit historical admission

A historical declaration carries the exact complete literal prefix before the final `-ordinal`, not newly resolved namespace coordinates. For example, `historical_prefix: rdr-Legacy-Project` admits `rdr-Legacy-Project-000042` unchanged. Supported historical prefixes are nonempty valid UTF-8 without whitespace or control characters; raw hyphens, uppercase, Unicode and literal percent spellings are matched byte-for-byte, never re-escaped or normalized. Historical ordinals may retain extra leading zeros but still require at least five decimal digits, a positive value and the uint64 ceiling. Other old formats remain unsupported and must not be silently converted.

This read-only branch admits syntax only, not every ordinal as an accepted record. Core must separately verify an accepted identity binding and its intended source. No legacy project is invented, and changing `mode` to `issuing` is refused unless the caller supplies a separate valid issuing namespace. An accepted cutover can retain old declarations beside a new issuing revision. It must not rewrite old UUIDs or UIDs, and this codec performs no cutover or migration.

## Scoped declaration-set checks

`ValidateSet` checks only the supplied inventory. Identical declarations may repeat for mappings of one shared sequence. Conflicting data for one org/sequence/revision, two active revisions of one sequence, independent active sequences with the same rendered prefix (including custom kinds sharing a prefix), and multiple full issuing sequences for one org/host/native-repository/kind are refused. It proves neither completeness of an org inventory nor that a source's host identity is verified.

Historical-only collisions on distinct sequences are permitted as explicit ambiguous history. A new independent issuer cannot reuse that occupied prefix. Matching historical and current prefixes on the same sequence remain legal. Parse always requires one caller-selected declaration; it does not choose the first matching declaration. A bare ambiguous historical UID still requires permission-scoped source/sequence plus UUID disambiguation in Core. Equal labels across distinct stable org identities are not globally unique and confer no cross-org access.

## Compatibility and verification

This schema is new. It does not reinterpret one-line `.arqtos/sequence`, old org-shaped labels, reservation-v1 or reference-catalog-v1 metadata. Unknown schema/profile/mode values are refused. Existing clients require an explicit pin and adapter; do not change an old schema's meaning because both happen to include the string `v1`.

Run `GOTOOLCHAIN=go1.27.1 go test ./recorduid -race -shuffle=on -count=1 -cover`, plus the two `FuzzDeclaration_...` targets, and `make ci` for the SDK. Tests cover golden profiles, custom kinds, native-byte preservation, wrong/missing coordinates, ordinal boundaries, literal history, declaration collisions and round trips. These are codec evidence, not qualification of Core callers, accepted reservations, cross-host custody or the complete standing claims.
