// Package codehost is the CodeHost connector-class contract: one adapter over
// one code host's repositories, its git transport, its branches and its
// change-request lifecycle.
//
// # Why this contract lives here and not in the SDK
//
// The SDK's connector.Class vocabulary is closed, and it names CodeHost as a
// class that lands with its design — connector.Classes() does not carry it at
// the pinned SDK version. Until it does, the contract lives in this
// repository, because a connector cannot be written against a class that has
// no types.
//
// It is written to the SDK's shapes, deliberately and everywhere, so that
// adopting the published version is a rename and not a rewrite:
//
//   - the base contract is connector.Connector, embedded, not restated;
//   - every failure is a *cerr.Error from the SDK's closed vocabulary, so a
//     host routes on the classification and never on message text. There is no
//     sentinel error and no error type of this package's own;
//   - every list operation returns [Resolution], which IS the SDK's
//     fail-closed list resolution — aliased, not re-implemented;
//   - optional operations sit behind capabilities and separate interfaces, the
//     way roster.Watcher sits behind roster.CapWatch.
//
// [Conform] is the harness for the class, for the same reason: the SDK ships
// credconform and rosterconform, and a class with no published harness is a
// class whose connectors are checked by nothing.
//
// # Transport is HTTPS with a caller-supplied token
//
// A connector in this class receives its token from its caller — at
// construction, in memory — and never reads one from the environment. The
// distinction is not stylistic. A connector that fell back to an ambient
// variable when its token was empty would resolve SOME credential in most
// environments, and act as whichever identity that variable happened to hold:
// not a failure, a silent substitution of identity. Refusing the empty token
// is the only behaviour that cannot do that.
package codehost

import (
	"context"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// The capability vocabulary of this class.
//
// Each one is a measured difference between real code hosts rather than a
// speculative flag, and each is declared in the connector's manifest AND
// reported by Capabilities(). A host reads the manifest before it loads
// anything, so an undeclared capability is one the host will never call.
const (
	// CapWebhooks declares that this connector can register a webhook
	// destination on a repository, via [WebhookRegistrar].
	//
	// It is optional because the administrative surface is where code hosts
	// diverge most: some expose repository webhooks to the token a connector
	// holds, some only to an org-level app installation, and some not at all
	// to a token without owner rights. A host that assumed it would fail
	// mid-provision on the second and third.
	CapWebhooks connector.Capability = "webhooks"

	// CapRunnerTokens declares that this connector can mint a registration
	// token for a self-hosted CI runner, via [RunnerTokenMinter].
	//
	// It is optional because the concept does not exist on every code host,
	// and where it does the token is short-lived and separately privileged. A
	// connector that cannot mint one must say so rather than leave a host to
	// infer it from a failure.
	CapRunnerTokens connector.Capability = "runner_tokens"

	// CapFileRead declares that this connector can read ONE file's bytes from
	// a repository's default branch without cloning it, via [FileReader].
	//
	// It is optional because it is a genuine API difference: reading a single
	// path without a clone needs a contents endpoint, and a connector over a
	// host without one would have to clone to answer — which is a different
	// operation with a different cost, not the same one.
	CapFileRead connector.Capability = "file_read"

	// CapNativeReview declares that change-request review happens on the code
	// host itself: approvals, review threads and merge gating live there, so a
	// host must not build a review surface of its own on top.
	//
	// Unlike the other three this capability has no operation behind it, and
	// [Conform] therefore cannot check it against a running connector — it is
	// checked only for being in this vocabulary. It is a declaration a host
	// reads and acts on, and it is recorded as such rather than dressed up as
	// something the harness verifies.
	CapNativeReview connector.Capability = "native_review"
)

// knownCapabilities is the closed capability vocabulary of this class. A
// manifest declaring anything outside it fails conformance: a capability the
// host does not recognise is a capability the host will not use, and a typo is
// indistinguishable from a capability that has yet to ship.
var knownCapabilities = connector.Capabilities{
	CapWebhooks, CapRunnerTokens, CapFileRead, CapNativeReview,
}

// KnownCapabilities returns the closed capability vocabulary for this class,
// as a copy. Adding one is a deliberate contract change.
func KnownCapabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), knownCapabilities...)
}

// A Resolution is what a list operation in this contract returns: either a
// list the connector actually read from the code host, or nothing at all.
//
// It is an ALIAS for the SDK's fail-closed list resolution, not a type of this
// package's own. The generic is published in the roster package because that
// class landed first, but the invariant it enforces is class-independent, and
// a second copy of it here would be a second thing to get wrong. Everything
// its documentation says holds verbatim for a list of repositories:
//
//   - the zero Resolution — what `return Resolution[Repo]{}, nil` produces —
//     is UNRESOLVED, and [Resolution.Items] refuses to read it. A failure path
//     therefore cannot hand back an empty list by accident;
//   - a code host that genuinely holds nothing is expressible only by SAYING
//     so, with [EmptyList]. Emptiness is asserted, never inferred from a
//     length;
//   - [Resolved] takes a [Completeness] on every call with no default, so a
//     pagination loop that broke off partway cannot be reported as a smaller
//     success. It must return a typed failure instead.
//
// That last one is the property a code host makes concrete. Listing an org's
// repositories is a paginated read, and the caller that gets 100 of 250 repos
// with no error reports CLEAN over a partial estate — a doctor check that
// found nothing wrong because it never looked at the rest.
type Resolution[T any] = roster.Resolution[T]

// A Completeness is the connector's assertion about whether the list it hands
// to [Resolved] is everything the operation is meant to report. It is the
// SDK's type; see [Complete] and [Partial].
type Completeness = roster.Completeness

const (
	// Complete asserts the list is everything the operation is meant to
	// report — a read whose pagination ran to its own natural end.
	Complete = roster.Complete
	// Partial asserts the list is only what a read covered before it stopped.
	// [Resolved] refuses to build a readable Resolution from one: a truncated
	// read is a typed failure, never a smaller success.
	Partial = roster.Partial
)

// Resolved wraps a list the connector actually read, asserting its
// [Completeness]. It forwards to the SDK; the Completeness argument is
// mandatory there and is deliberately not defaulted here, because the whole
// value of the type is that no call site can assert a complete read by
// omission.
//
// It returns an unreadable Resolution and an error when items is empty (say so
// with [EmptyList] instead) or when c is not [Complete].
func Resolved[T any](items []T, c Completeness) (Resolution[T], error) {
	return roster.Resolved(items, c)
}

// EmptyList reports a code host that genuinely, verifiably holds none of the
// requested thing — an org with no repositories, a repo with no open change
// requests. It forwards to the SDK.
//
// This is an ASSERTION, not a fallback. Call it only where the backend
// distinguishes "read successfully, found none" from "could not read": an HTTP
// 200 carrying an empty array is the first, and anything else is not.
func EmptyList[T any]() Resolution[T] { return roster.EmptyRoster[T]() }

// A Repo is one repository as the code host holds it.
type Repo struct {
	// FullName is the host's `<owner>/<name>` identifier.
	FullName string
	// Owner is the org or user the repository belongs to.
	Owner string
	// Name is the repository name within the owner.
	Name string
	// Private is whether the host considers the repository non-public.
	Private bool
	// Topics are the repository's topic labels, as the host holds them.
	Topics []string
}

// CreateRepoOpts describes a repository to create.
type CreateRepoOpts struct {
	Owner       string
	Name        string
	Private     bool
	Description string
	Topics      []string
}

// A Branch is a named branch tip.
type Branch struct {
	Name string
	SHA  string
}

// CodeHost is the connector-class contract.
//
// Every failure it returns is typed: a *cerr.Error whose Kind comes from
// cerr's closed vocabulary, so a host acts on the classification and never on
// the message. A failure the connector cannot classify is cerr.KindUnknown,
// which fails the call and escalates nothing.
//
// No list operation can report a success carrying no list — see [Resolution].
// No operation returns a pointer that a caller must nil-check: a (*Repo, nil)
// return is the same conflation in another shape, so the single-item reads
// return values.
//
// Optional operations live behind capabilities rather than in this interface:
// [FileReader] behind [CapFileRead], [WebhookRegistrar] behind [CapWebhooks],
// [RunnerTokenMinter] behind [CapRunnerTokens]. A host type-asserts for them,
// and [github.com/arqtiqa/arqtos-sdk-go/codehostconform.Run] fails a connector
// that declares one without implementing it — in both directions.
type CodeHost interface {
	connector.Connector

	// RepoExists reports whether fullName (`<owner>/<name>`) is reachable
	// with the identity this connector holds.
	//
	// The bool is meaningful ONLY when the error is nil. false means the host
	// answered that the repository is not there; a failure to look is the
	// error, never false.
	RepoExists(ctx context.Context, fullName string) (bool, error)

	// GetRepo returns the current state of an existing repository.
	// A repository that does not exist is cerr.KindNotFound.
	GetRepo(ctx context.Context, fullName string) (Repo, error)

	// ListRepos returns every repository owned by owner — an org or a user —
	// with topics populated.
	//
	// It MUST page until the host reports no further pages. An owner with more
	// repositories than one page holds is the ordinary case, and a caller
	// handed the first page and no error reports clean over a partial estate.
	// A read that stops early is a typed failure, not a shorter list: see
	// [Resolution].
	ListRepos(ctx context.Context, owner string) (Resolution[Repo], error)

	// CreateRepo creates a repository and returns it as the host now holds
	// it, including any topics that were applied.
	CreateRepo(ctx context.Context, opts CreateRepoOpts) (Repo, error)

	// SetTopics replaces the repository's topic labels with topics.
	SetTopics(ctx context.Context, fullName string, topics []string) error

	// CloneRepo clones the repository at url into dest over HTTPS.
	//
	// The token reaches git for the duration of this one command and no
	// longer: not in the process's arguments, not written to any git config,
	// and not left in an operating-system keychain. A connector that cannot
	// promise that must not implement this operation.
	CloneRepo(ctx context.Context, url, dest string) error

	// PushBranch pushes branch from the working tree at localDir to its
	// origin. fullName identifies the repository for connectors that push
	// through an API rather than through git.
	PushBranch(ctx context.Context, fullName, localDir, branch string) error

	// ListBranches returns every branch of the named repository, paging until
	// the host reports no further pages.
	ListBranches(ctx context.Context, fullName string) (Resolution[Branch], error)

	// ⚠️ The change-request surface is NOT here. Opening, reading, listing,
	// commenting on and merging a change all belong to the CodeCI class, ruled
	// 2026-08-12: this class owns repositories and git, and a change proposal is
	// neither. Before that ruling both classes carried part of it — this one
	// could open and comment but not merge, CodeCI could merge and diff but not
	// read one back — and neither was a subset of the other.
	//
	// ⚠️ ListBranches above stays in BOTH classes deliberately. Every class must
	// be self-sufficient: a CodeCI connector cannot call a CodeHost one, so each
	// carries the branch read it needs. Two classes needing the same FACT
	// independently is legitimate; two classes owning the same RESPONSIBILITY was
	// the defect the ruling removed.
}

// FileReader is the optional operation behind [CapFileRead]: read one file
// from a repository's default branch without cloning it.
//
// A connector that can do this does two things, and must do both: implement
// this interface, and declare [CapFileRead] in its manifest and from
// Capabilities(). Declaring without implementing is worse than declaring
// nothing — the host plans a cheap read and finds nothing there.
type FileReader interface {
	// ReadFile returns path's bytes from fullName's DEFAULT branch.
	//
	// A repository that is reachable but does not carry path is
	// cerr.KindNotFound. Every other failure is a failure to LOOK, and must
	// carry its own kind: a caller that read "could not look" as "not there"
	// would conclude a file had been deleted because a token expired.
	ReadFile(ctx context.Context, fullName, path string) ([]byte, error)
}

// WebhookRegistrar is the optional operation behind [CapWebhooks].
type WebhookRegistrar interface {
	// WebhookRegister registers url as a destination for the named events on
	// the repository.
	WebhookRegister(ctx context.Context, fullName, url string, events []string) error
}

// RunnerTokenMinter is the optional operation behind [CapRunnerTokens].
type RunnerTokenMinter interface {
	// RunnerToken mints a registration token for a self-hosted CI runner on
	// the repository, and reports when it expires.
	//
	// The returned token is secret material with a short life. A caller hands
	// it to the runner it is registering and keeps no copy; this contract does
	// not admit storing it.
	RunnerToken(ctx context.Context, fullName string) (token string, expiresAt time.Time, err error)
}
