// Package codeci defines the CodeCI connector-class contract: pull/merge
// request lifecycle, diffs, branch listing, CI check/workflow-run operations,
// and the two issue operations below, against ONE code host's PR/CI surface.
//
// # Why issues are here at all, and where that stops
//
// An issue is a repository object, reached the same way every other object in
// this class is reached: by `<owner>/<name>` plus an id. It lives in a repo on
// GitHub and on GitLab alike, and it is the thing a pull request closes.
//
// It is here because two callers need an issue's state, and its close, WITHOUT
// a board: "is the issue this work is blocked on still open" and "close the
// issue whose work has shipped". Neither question mentions a board, and the
// tracker class cannot express them — [github.com/arqtiqa/arqtos-sdk-go/tracker]
// addresses an item as {board, scope, number} and refuses an address missing any
// component rather than guessing the rest from context, so an issue on a
// repository whose issues are on NO board has no address there at all. A
// board's view of whether an item is open is a different fact from whether the
// issue is open, not a smaller one.
//
// ⚠️ The boundary, because this is the operation set most likely to grow:
// **no issue LISTING and no arbitrary issue FIELD WRITES.** Two operations,
// both by explicit address. What that refuses is a second work tracker growing
// inside this class — the shape [github.com/arqtiqa/arqtos-sdk-go/tracker]'s own
// package doc records as the cause of a fifty-operation board update becoming an
// 1,850-request sweep, when an abstract backend grew one method per vendor API.
// A caller that wants to search issues, or set a label or an assignee, wants the
// tracker class, and if the tracker class cannot serve it then that is a finding
// about the tracker class rather than a reason to widen this one.
//
// # Why both issue operations are required, and not behind a capability
//
// [CodeCI.GetIssue] and [CodeCI.CloseIssue] are in the required interface.
//
// For the READ, the argument [CodeCI.WhoAmI] made transfers, and it lands
// harder here: an assertion a host can only make for SOME connectors is not an
// assertion, it is a fallback path. A blocker-resolution path that exists on
// some code hosts is not a blocker-resolution path — a caller would have to
// keep a per-host arm for the hosts that lack it, which is precisely the
// duplication this class exists to remove.
//
// For the CLOSE, the decisive fact is that this class ALREADY holds write
// authority on the same repositories: [CodeCI.MergePR] is a required write, and
// it is a far larger one. So a close is not new authority to gate, it is
// authority this class already assumes. Compare [CIController], which IS gated:
// mutating a CI run is a separately privileged vendor scope, granted apart from
// the scope that reads it. Closing an issue is not — a credential that can
// merge into a repository can close an issue in it.
//
// The vendors' inability to record a close REASON uniformly is not a reason to
// gate either. Gating on that would make the close itself conditional, when the
// close is the postcondition the caller needs and every host supports it. It is
// documented as a vendor limit on [CodeCI.CloseIssue] instead, the same way
// [DiffFile.Patch] documents its own.
//
// # Out-of-process: this class is native-only, including these two
//
// This class has NO wire protocol, and these operations do not add one.
// [CodeCI] carries no .proto and no out-of-process binding — the same position
// the roster and credential-loader classes each held before their wire
// protocols landed as separate, deliberate pieces of work. So a CodeCI
// connector is in-process and compiled into the host, and an external
// developer cannot implement this class out-of-process today.
//
// That is stated here rather than left to be discovered: publishing a wire
// binding for two operations while the other ten have none would leave the
// class half-reachable over RPC, which is worse than not reachable at all. When
// this class does get a wire protocol it covers the class, and these two travel
// with it.
//
// # Why this is a separate class from code-host administration
//
// A code host is reached for two genuinely different reasons: administering
// repositories (create, set topics, register webhooks, mint runner tokens,
// clone/push over git) and reviewing/running change requests (open a PR,
// merge it, read its diff, check whether CI is green). Same vendor, different
// contract — and the split matters in practice, not just in principle: an org
// can run its own code host for repository administration while pairing it
// with a different vendor, or a different token altogether, for PR review and
// CI. Collapsing the two into one connector would make that combination
// unexpressible. This package is the second half only; the first is a
// separate class.
//
// # Derived from two vendors, not one
//
// GitHub and GitLab both expose pull/merge requests, CI runs, branches and
// diffs — through very different APIs (GitHub Actions has no rich GraphQL
// surface for workflow runs; GitLab's CI model is pipelines/jobs, not
// checks/workflow-runs). This contract names operations at the level of what
// a caller wants ("what is the CI status of this ref", "merge this PR"), not
// at the level of how one vendor happens to implement it — an ABC derived
// from a single backend reliably encodes that backend's accidents as
// contract. Where this package is still confident the shape holds across both
// (open/list/merge a change request, read its diff, list branches, read
// check/run status), the operation is in the required [CodeCI] interface.
// Where a real difference in what tokens/installations can do is plausible —
// mutating a CI run versus merely reading its status — the operation sits
// behind a capability, the same way the roster and codehost classes gate
// theirs.
//
// This is written against GitHub's current, in-production PR/CI surface and
// against GitLab's well-documented REST API; it has not yet been proven
// against a running GitLab connector. Treat the shape as confident, not
// closed: a GitLab implementation that reveals a genuine further divergence
// gets a new capability, not a reshaped required method.
//
// # Transport is forced apart by the vendor, and that is not this contract's
// business
//
// GitHub answers "what is the CI status of a ref" over GraphQL and "rerun
// this workflow" over REST, because Actions has no rich GraphQL surface for
// the second. That split is real and an implementation MUST NOT paper over it
// by inventing a unified transport that does not exist on the vendor's side —
// but it is invisible here. [CodeCI]'s methods do not know or care which
// transport an implementation uses for which operation; "one GraphQL client"
// means an implementation MUST NOT hand-roll a second GraphQL client where a
// typed one already exists in its module, not that every operation must go
// over the same wire protocol.
//
// # One token chain
//
// A connector in this class receives its credential material from a
// [github.com/arqtiqa/arqtos-sdk-go/credential.CredentialLoader] at
// construction, in memory, and never reads one from the environment — the
// same discipline the codehost class documents and for the same reason: a
// connector that fell back to an ambient variable when its token was empty
// would authenticate as whichever identity that variable happened to hold,
// silently, in most environments. There is exactly one place a token
// resolves — through the broker — so there is exactly one place an operator
// looks when a credential is wrong.
//
// # Fail-closed lists, the same way roster's are
//
// [CodeCI.ListPRs], [CodeCI.GetDiff], [CodeCI.ListBranches] and
// [CodeCI.GetCheckRuns] all return [Resolution], not a slice. A caller
// deciding "does this ref have any failing checks" from a truncated read that
// looked like a complete one would merge on an answer it never actually
// computed — the same shape of harm the roster class's Resolution exists to
// remove, here landing on CI safety rather than on access. See [Resolution].
package codeci

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// The capability vocabulary of this class.
//
// Each one is declared in the connector's manifest AND reported by
// Capabilities(), and checked against the running connector by
// [github.com/arqtiqa/arqtos-sdk-go/codeciconform].
const (
	// CapCIControl declares that this connector can MUTATE a CI run — rerun
	// or cancel it — via [CIController], in addition to reading its status.
	//
	// It is optional because reading and mutating CI are plausibly different
	// permissions: a token or app installation scoped for review can read
	// check status and pipeline state without holding the (separately
	// privileged, write-scoped) permission a rerun or cancel needs — the same
	// reasoning the codehost class's CapRunnerTokens is built on. A connector
	// without it MUST still implement the read-only CI operations in
	// [CodeCI]; it simply has nothing behind [CIController].
	CapCIControl connector.Capability = "ci_control"

	// CapCheckPublish declares that this connector can PUBLISH a check state
	// against a commit — create or update its own check — via
	// [CheckPublisher], in addition to reading the checks others published.
	//
	// It is optional, and separately so from [CapCIControl], because the two
	// are different permissions rather than two halves of one. Re-running a
	// workflow acts on a run the connector did not create; publishing a check
	// writes one the connector's own identity owns. A credential can hold
	// either without the other, so collapsing them would make a host that
	// checked one capability act on a wrong assumption about the other.
	CapCheckPublish connector.Capability = "check_publish"
)

// knownCapabilities is the closed capability vocabulary of this class. A
// manifest declaring anything outside it fails conformance: a capability the
// host does not recognise is a capability the host will not use, and a typo
// is indistinguishable from a capability that has yet to ship.
var knownCapabilities = connector.Capabilities{
	CapCIControl, CapCheckPublish,
}

// KnownCapabilities returns the closed capability vocabulary for the CodeCI
// class, as a copy. Adding one is a deliberate contract change.
func KnownCapabilities() connector.Capabilities {
	return append(connector.Capabilities(nil), knownCapabilities...)
}

// A PRState is a pull/merge request's state, used both as a filter for
// [CodeCI.ListPRs] and as the state reported on a [PR].
//
// It is an integer type whose zero value means "nothing was said", rather
// than a string type whose zero value is "". A filter nobody set must not
// silently read as one particular state — see [PRStateUnspecified].
type PRState int

const (
	// PRStateUnspecified is the zero value: no state was named. It is not a
	// usable filter — see [PRState.UsableAsFilter] — and [CodeCI.ListPRs]
	// refuses it with cerr.KindInvalid rather than picking one. A forgotten
	// argument that quietly meant "open" would have a caller sweeping for
	// merged work and finding none.
	PRStateUnspecified PRState = iota
	// PRStateOpen is a pull/merge request still open.
	PRStateOpen
	// PRStateClosed is one closed without merging.
	PRStateClosed
	// PRStateMerged is one that was merged.
	PRStateMerged
	// PRStateAny matches every state. It is a filter only; no [PR] is ever
	// reported with it.
	PRStateAny
)

// prStateNames is the single source of truth for the closed PRState
// vocabulary: [PRStates], [PRState.Valid] and [PRState.String] all derive
// from it, so a state cannot be half-added.
var prStateNames = map[PRState]string{
	PRStateUnspecified: "unspecified",
	PRStateOpen:        "open",
	PRStateClosed:      "closed",
	PRStateMerged:      "merged",
	PRStateAny:         "any",
}

var prStates = func() []PRState {
	out := make([]PRState, 0, len(prStateNames))
	for s := range prStateNames {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}()

// PRStates returns the closed PRState vocabulary, in ascending order, as a copy.
func PRStates() []PRState { return slices.Clone(prStates) }

// Valid reports whether s names a state in the closed vocabulary.
// PRStateUnspecified is valid as a VALUE and still refused as a FILTER — see
// [PRState.UsableAsFilter].
func (s PRState) Valid() bool {
	_, ok := prStateNames[s]
	return ok
}

// UsableAsFilter reports whether s can be passed to [CodeCI.ListPRs]: a state
// in the vocabulary that is not [PRStateUnspecified].
func (s PRState) UsableAsFilter() bool { return s.Valid() && s != PRStateUnspecified }

// String renders s's stable name. A value outside the vocabulary renders as
// invalid_pr_state(N) rather than as any real state, so a message built from
// it names what was actually passed.
func (s PRState) String() string {
	if name, ok := prStateNames[s]; ok {
		return name
	}
	return "invalid_pr_state(" + strconv.Itoa(int(s)) + ")"
}

// A MergeMethod is how a pull/merge request is integrated. It is the argument
// to [CodeCI.MergePR].
type MergeMethod int

const (
	// MergeMethodUnspecified is the zero value: no method was named.
	// [CodeCI.MergePR] MUST refuse it with cerr.KindInvalid rather than
	// picking a default — see [MergeMethod.Specified].
	MergeMethodUnspecified MergeMethod = iota
	// MergeMethodMerge creates an ordinary merge commit.
	MergeMethodMerge
	// MergeMethodSquash squashes the change into a single commit.
	MergeMethodSquash
	// MergeMethodRebase rebases the change onto the base branch with no merge commit.
	MergeMethodRebase
)

var mergeMethodNames = map[MergeMethod]string{
	MergeMethodUnspecified: "unspecified",
	MergeMethodMerge:       "merge",
	MergeMethodSquash:      "squash",
	MergeMethodRebase:      "rebase",
}

var mergeMethods = func() []MergeMethod {
	out := make([]MergeMethod, 0, len(mergeMethodNames))
	for m := range mergeMethodNames {
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}()

// MergeMethods returns the closed MergeMethod vocabulary, in ascending order, as a copy.
func MergeMethods() []MergeMethod { return slices.Clone(mergeMethods) }

// Valid reports whether m names a method in the closed vocabulary.
// MergeMethodUnspecified is valid as a VALUE and still refused when passed to
// [CodeCI.MergePR] — see [MergeMethod.Specified].
func (m MergeMethod) Valid() bool {
	_, ok := mergeMethodNames[m]
	return ok
}

// Specified reports whether m can be passed to [CodeCI.MergePR]: a method in
// the vocabulary that is not [MergeMethodUnspecified].
func (m MergeMethod) Specified() bool { return m.Valid() && m != MergeMethodUnspecified }

// String renders m's stable name. A value outside the vocabulary renders as
// invalid_merge_method(N), so a message built from it names what was
// actually passed rather than assuming one of the three real methods.
func (m MergeMethod) String() string {
	if name, ok := mergeMethodNames[m]; ok {
		return name
	}
	return "invalid_merge_method(" + strconv.Itoa(int(m)) + ")"
}

// A FileStatus is what happened to one file between a pull/merge request's
// base and its head.
type FileStatus int

const (
	// FileStatusUnspecified is the zero value: a connector that forgets to
	// set it reports "unclassified" rather than silently reporting "added".
	FileStatusUnspecified FileStatus = iota
	FileStatusAdded
	FileStatusModified
	FileStatusRemoved
	FileStatusRenamed
)

var fileStatusNames = map[FileStatus]string{
	FileStatusUnspecified: "unspecified",
	FileStatusAdded:       "added",
	FileStatusModified:    "modified",
	FileStatusRemoved:     "removed",
	FileStatusRenamed:     "renamed",
}

var fileStatuses = func() []FileStatus {
	out := make([]FileStatus, 0, len(fileStatusNames))
	for s := range fileStatusNames {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}()

// FileStatuses returns the closed FileStatus vocabulary, in ascending order, as a copy.
func FileStatuses() []FileStatus { return slices.Clone(fileStatuses) }

// Valid reports whether s is in the closed vocabulary.
func (s FileStatus) Valid() bool {
	_, ok := fileStatusNames[s]
	return ok
}

// String renders s's stable name. A value outside the vocabulary renders as
// invalid_file_status(N) rather than as "unspecified".
func (s FileStatus) String() string {
	if name, ok := fileStatusNames[s]; ok {
		return name
	}
	return "invalid_file_status(" + strconv.Itoa(int(s)) + ")"
}

// A RunStatus is the state of one CI check or workflow/pipeline run, used by
// both [CheckRun] and [WorkflowRun].
//
// It deliberately merges GitHub's separate status/conclusion pair (queued,
// in_progress, completed × success, failure, neutral, cancelled, skipped,
// timed_out, action_required, stale) and GitLab's pipeline/job status
// (created, pending, running, success, failed, canceled, skipped, manual,
// ...) into one closed vocabulary: a caller asking "did this pass" wants one
// answer, not a status and a conclusion it must cross-reference itself. A
// connector maps its vendor's finer states down onto this one; it must not
// invent a value outside it.
type RunStatus int

const (
	// RunStatusUnspecified is the zero value: a connector that forgets to set
	// it reports "unclassified" rather than accidentally reporting success.
	RunStatusUnspecified RunStatus = iota
	// RunStatusPending covers a run that has been created but has not started
	// executing (GitHub: queued; GitLab: created/pending/preparing/
	// waiting_for_resource/scheduled).
	RunStatusPending
	// RunStatusRunning is a run currently executing.
	RunStatusRunning
	RunStatusSuccess
	RunStatusFailure
	RunStatusCancelled
	RunStatusSkipped
	// RunStatusActionRequired is a run waiting on a human decision (GitHub:
	// action_required; GitLab: manual).
	RunStatusActionRequired
	RunStatusTimedOut
)

var runStatusNames = map[RunStatus]string{
	RunStatusUnspecified:    "unspecified",
	RunStatusPending:        "pending",
	RunStatusRunning:        "running",
	RunStatusSuccess:        "success",
	RunStatusFailure:        "failure",
	RunStatusCancelled:      "cancelled",
	RunStatusSkipped:        "skipped",
	RunStatusActionRequired: "action_required",
	RunStatusTimedOut:       "timed_out",
}

var runStatuses = func() []RunStatus {
	out := make([]RunStatus, 0, len(runStatusNames))
	for s := range runStatusNames {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}()

// RunStatuses returns the closed RunStatus vocabulary, in ascending order, as a copy.
func RunStatuses() []RunStatus { return slices.Clone(runStatuses) }

// Valid reports whether s is in the closed vocabulary.
func (s RunStatus) Valid() bool {
	_, ok := runStatusNames[s]
	return ok
}

// String renders s's stable name. A value outside the vocabulary renders as
// invalid_run_status(N) rather than as "unspecified".
func (s RunStatus) String() string {
	if name, ok := runStatusNames[s]; ok {
		return name
	}
	return "invalid_run_status(" + strconv.Itoa(int(s)) + ")"
}

// IssueState is whether an issue is open or closed, as the code host reports it.
//
// ⚠️ This is deliberately a three-value vocabulary and NOT an `Open bool`,
// for the reason [Branch.Protected] documents: a bool's zero value asserts one
// of the two real answers. `Open == false` reads as "this issue is closed",
// which is the direction that gets an operator told to start work that is
// still blocked. The zero value here asserts NEITHER answer.
//
// Two layers keep an unresolvable issue from reading as closed. An issue that
// does not exist, or that the credential cannot see, is a typed failure from
// [CodeCI.GetIssue] and never a value at all — that is the layer that matters.
// IssueStateUnspecified is the second: a connector that returns a success
// having never looked has violated the contract, and it renders as
// "unspecified" rather than as either real state, so the violation is visible
// instead of being read as an answer.
type IssueState int

const (
	// IssueStateUnspecified is the zero value: it asserts nothing. A connector
	// MUST NOT return it from a successful GetIssue — an issue whose state
	// cannot be determined fails instead.
	IssueStateUnspecified IssueState = iota
	// IssueStateOpen is an issue the code host reports as open.
	IssueStateOpen
	// IssueStateClosed is an issue the code host reports as closed, whatever the
	// reason it was closed for.
	IssueStateClosed
)

var issueStateNames = map[IssueState]string{
	IssueStateUnspecified: "unspecified",
	IssueStateOpen:        "open",
	IssueStateClosed:      "closed",
}

var issueStates = func() []IssueState {
	out := make([]IssueState, 0, len(issueStateNames))
	for s := range issueStateNames {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}()

// IssueStates returns the closed IssueState vocabulary, in ascending order, as a copy.
func IssueStates() []IssueState { return slices.Clone(issueStates) }

// Valid reports whether s is in the closed vocabulary.
func (s IssueState) Valid() bool {
	_, ok := issueStateNames[s]
	return ok
}

// Determined reports whether s names a real state a code host reported —
// the check a connector's own GetIssue MUST pass before returning success, and
// the one a caller reading a blocker's state MUST make before believing it.
func (s IssueState) Determined() bool { return s.Valid() && s != IssueStateUnspecified }

// String renders s's stable name. A value outside the vocabulary renders as
// invalid_issue_state(N) rather than as "unspecified".
func (s IssueState) String() string {
	if name, ok := issueStateNames[s]; ok {
		return name
	}
	return "invalid_issue_state(" + strconv.Itoa(int(s)) + ")"
}

// CloseReason is why an issue is being closed: it was completed, or it was
// abandoned. The distinction is not cosmetic — an audit trail that cannot tell
// "we did this" from "we decided not to" has lost the more interesting half.
//
// ⚠️ This vocabulary is this package's own, and it is NOT
// [github.com/arqtiqa/arqtos-sdk-go/tracker.CloseReason]. The two packages are
// deliberately independent — neither imports the other, so either class can be
// implemented, versioned and reasoned about without the other — and a shared
// vocabulary would be exactly the edge that ends. They name the same
// distinction because the distinction is real, not because one is a copy of the
// other.
//
// ⚠️ A caller holding both MUST map between them BY NAME. Both are `int` with
// coinciding orders today, so `codeci.CloseReason(trackerReason)` compiles and
// happens to be correct — and would keep compiling, silently mis-mapping, the
// first time either vocabulary gains a value in a different position. Neither
// package can stop that conversion; both can refuse to make it look safe.
type CloseReason int

const (
	// CloseReasonUnspecified is the zero value. [CodeCI.CloseIssue] MUST refuse
	// it: a close that names no reason is a caller that has not decided, and
	// recording "completed" on its behalf is a guess written into an audit
	// trail.
	CloseReasonUnspecified CloseReason = iota
	// CloseReasonCompleted closes an issue whose work was done.
	CloseReasonCompleted
	// CloseReasonCanceled closes an issue whose work will not be done.
	CloseReasonCanceled
)

var closeReasonNames = map[CloseReason]string{
	CloseReasonUnspecified: "unspecified",
	CloseReasonCompleted:   "completed",
	CloseReasonCanceled:    "canceled",
}

var closeReasons = func() []CloseReason {
	out := make([]CloseReason, 0, len(closeReasonNames))
	for r := range closeReasonNames {
		out = append(out, r)
	}
	slices.Sort(out)
	return out
}()

// CloseReasons returns the closed CloseReason vocabulary, in ascending order, as a copy.
func CloseReasons() []CloseReason { return slices.Clone(closeReasons) }

// Valid reports whether r is in the closed vocabulary.
func (r CloseReason) Valid() bool {
	_, ok := closeReasonNames[r]
	return ok
}

// UsableAsReason reports whether r can be passed to [CodeCI.CloseIssue] — the
// check that method MUST make before anything else.
func (r CloseReason) UsableAsReason() bool { return r.Valid() && r != CloseReasonUnspecified }

// String renders r's stable name. A value outside the vocabulary renders as
// invalid_close_reason(N) rather than as "unspecified".
func (r CloseReason) String() string {
	if name, ok := closeReasonNames[r]; ok {
		return name
	}
	return "invalid_close_reason(" + strconv.Itoa(int(r)) + ")"
}

// A PR is one pull/merge request as the code host holds it.
type PR struct {
	// ID is the host's identifier for this pull/merge request, as a string —
	// GitHub numbers PRs per repository; GitLab's merge requests carry both a
	// repo-scoped iid and a global id, and this is whichever one the
	// connector's own Get/List/Merge/GetDiff calls agree on. A caller passes
	// it back verbatim and never parses or formats it.
	ID string
	// FullName is the `<owner>/<name>` of the repository it is against.
	FullName string
	// Branch is the source (head) branch.
	Branch string
	// BaseBranch is the target branch it merges into.
	BaseBranch string
	Title      string
	Body       string
	State      PRState
	// Draft reports whether the code host considers this a draft/WIP
	// pull/merge request — a state [CodeCI.MergePR] MUST refuse to merge and
	// [CodeCI.CreatePR] can open, via [CreatePRRequest.Draft].
	Draft bool
	// URL is the pull/merge request's own page on the code host.
	//
	// It MUST be non-empty. Every code host this class is written against
	// gives a change request a web address, and it is derivable from the
	// vendor's URL layout — but a contract that leaves it to the caller has
	// pushed vendor knowledge back out to every caller, which is the
	// duplication this class exists to remove. Unlike [CheckRun.DetailsURL],
	// which a host may genuinely not provide, this one is always available.
	URL string
}

// A CreatePRRequest is the pull/merge request [CodeCI.CreatePR] is asked to
// open.
//
// # Why a struct rather than positional arguments
//
// CreatePR previously took five strings after ctx — fullName, branch, base,
// title, body — all five adjacent and interchangeable to the compiler:
// transposing base and branch, or title and body, builds cleanly and opens the
// wrong pull request. A sixth positional argument would have kept that hazard
// and lengthened it. A struct names every field at the call site, and — the
// reason that matters more for a PUBLISHED contract — the NEXT field this
// request needs (reviewers, labels, a maintainer-edit flag) is an additive
// change that breaks no caller, where a seventh parameter would reopen this
// signature again.
//
// # What a struct costs, and how that cost is paid
//
// Positional arguments force a caller to write "" on purpose; a struct lets a
// field be forgotten silently, which would put a zero value straight back into
// a contract built to refuse them. That is why [CreatePRRequest.Validate]
// exists and why [CodeCI.CreatePR] MUST call it before it does anything else —
// the same guard, in the same place, that MergePR already applies to
// [MergeMethod].
type CreatePRRequest struct {
	// FullName is the `<owner>/<name>` of the repository to open it against.
	FullName string
	// Branch is the source (head) branch the change is on.
	Branch string
	// Base is the target branch it should merge into.
	Base string
	// Title is the pull/merge request's title.
	Title string
	// Body is its description. It is OPTIONAL: a change request with no prose
	// is an ordinary thing to open, so an empty Body is a real value rather
	// than a forgotten one.
	Body string
	// Draft asks the code host to open this as a draft/WIP pull request.
	//
	// It is optional and its zero value is ready-for-review, which is the
	// answer a caller that says nothing means. This is the field
	// issue #46 adds: before it, the contract could READ draft state
	// (see [PR.Draft]) and REFUSE to merge one (see [CodeCI.MergePR]) but had
	// no way to produce one — it observed and gated a state it could not
	// create.
	Draft bool
}

// requiredCreatePRFields is the single source of truth for what a request must
// carry: [CreatePRRequest.Validate] derives from it, so a field cannot be
// half-required. The names are the wire names a host reports, not the Go field
// names, so an operator reading the refusal sees what they typed.
var requiredCreatePRFields = []struct {
	name string
	get  func(CreatePRRequest) string
}{
	{"full_name", func(r CreatePRRequest) string { return r.FullName }},
	{"branch", func(r CreatePRRequest) string { return r.Branch }},
	{"base", func(r CreatePRRequest) string { return r.Base }},
	{"title", func(r CreatePRRequest) string { return r.Title }},
}

// Validate reports whether r carries everything needed to open a pull/merge
// request, as a cerr.KindInvalid failure naming EVERY missing field rather than
// the first — a caller fixing a request one round trip per field learns the
// same thing four times as slowly.
//
// Body and Draft are not checked: both have real, meaningful zero values.
func (r CreatePRRequest) Validate() error {
	var missing []string
	for _, f := range requiredCreatePRFields {
		if f.get(r) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return cerr.New(cerr.KindInvalid, "CreatePR", fmt.Errorf(
		"pull/merge request is missing required field(s) %s; a struct field left unset is not a smaller request",
		strings.Join(missing, ", ")))
}

// An Identity is the account a [CodeCI] connector's own credential
// authenticates as, as the CODE HOST reports it — never as the connector or a
// host assumes it to be.
//
// # Why this is a contract operation and not a host's own probe
//
// A host publishes this answer as the standing proof that it authenticates with
// no token in its own environment: the credential came through the broker, the
// code host was asked who is calling, and it named an account. A host that
// produced that answer itself would be asserting the claim rather than
// measuring it, and a host that served it from
// [connector.Connector.Health] — which reports reachability and no login —
// would publish an empty login beside a success, destroying the assertion it
// exists to make.
type Identity struct {
	// Login is the account name the code host answers with.
	//
	// It MUST be non-empty whenever Authenticated is true. An empty login
	// reported alongside a success is not a smaller answer — it is the
	// opposite of the answer, wearing a success's shape. A connector that
	// cannot learn the login returns a typed failure instead
	// (cerr.KindUnauthorized, cerr.KindUnavailable).
	Login string
	// Authenticated reports whether the code host recognised the credential as
	// some account at all.
	//
	// False is a real answer rather than a failure, and it is narrow: the code
	// host ANSWERED and identified the caller as nobody, which a host serving
	// anonymous reads does. A credential the code host REJECTED is not this —
	// that is cerr.KindUnauthorized, because a rejected credential is a failed
	// read and not a report about an anonymous one. When it is false, Login is
	// empty: there is no account to name.
	Authenticated bool
}

// Coherent reports whether i says one thing rather than two contradictory
// ones. It is the single source of truth for the invariant [CheckIdentity] and
// the codeciconform harness both enforce, so neither restates it.
//
// Two shapes are incoherent: an authenticated identity carrying no login (the
// shape that falsifies the assertion an identity probe exists to make), and a
// login named while denying authentication ("I am nobody, and nobody is called
// octocat").
func (i Identity) Coherent() bool { return i.Authenticated == (i.Login != "") }

// A DiffFile is one file's change within a pull/merge request, as returned by
// [CodeCI.GetDiff].
type DiffFile struct {
	// Path is the file's path. For a rename, this is the NEW path.
	Path      string
	Status    FileStatus
	Additions int
	Deletions int
	// Patch is the unified-diff text for this file, where the code host
	// provides one. It MAY be empty for a binary file or one too large for
	// the host to render a patch for — that is a vendor limit, not a failure.
	Patch string
}

// A Branch is a named branch tip.
type Branch struct {
	Name string
	SHA  string
	// Protected reports whether the code host enforces protection rules on
	// this branch — required reviews, required checks, a push restriction:
	// whatever the vendor calls the state in which a push or merge is not
	// simply allowed.
	//
	// ⚠️ A connector MUST report what the code host says and MUST NOT leave
	// this at its zero value because it did not look. false reads as "this
	// branch is unprotected", which is the one always-zero field in this
	// package that is actively dangerous: a caller checking whether it may
	// force-push gets a yes it never computed. A connector that cannot
	// determine protection state returns a typed failure from
	// [CodeCI.ListBranches] rather than a list of branches asserting they are
	// all unprotected.
	Protected bool
}

// A CheckRun is one check/status entry reported against a commit ref.
type CheckRun struct {
	ID     string
	Name   string
	Status RunStatus
	// DetailsURL points at the check's own report, where the code host
	// provides one. It MAY be empty.
	DetailsURL string
}

// A WorkflowRun is one CI automation run — a GitHub Actions workflow run, a
// GitLab pipeline — as [CodeCI.GetWorkflowRun] reports it.
type WorkflowRun struct {
	ID     string
	Name   string
	Status RunStatus
	// Branch is the branch the run executed against.
	Branch string
	// CommitSHA is the commit the run executed against.
	CommitSHA string
	// URL points at the run's own page on the code host, where one exists.
	URL string
}

// An Issue is one repository issue as [CodeCI.GetIssue] reports it — the object
// a pull/merge request closes, addressed the way everything else in this class
// is addressed: repository plus id.
//
// ⚠️ It carries the state and the address, and almost nothing else, ON PURPOSE.
// This class holds two issue operations and no issue search; a type that grew
// labels, assignees and milestones would be advertising a tracker this class has
// deliberately refused to become — see the package doc for the boundary and what
// it costs to lose it.
type Issue struct {
	// FullName is the "<owner>/<name>" the issue was read from, echoed back.
	//
	// It is here so a caller holding several resolved issues cannot mix them up,
	// and so a connector answering the wrong repository is detectable rather than
	// plausible. A bare state carries no evidence of what it is the state OF.
	FullName string
	// Number is the issue's number within that repository, echoed back — an int,
	// matching how both code hosts number issues and how a blocker written "#N"
	// in prose refers to one.
	Number int
	// State is what the code host says. It MUST be [IssueState.Determined]: a
	// success carrying IssueStateUnspecified is a contract violation, because a
	// caller resolving a blocker would read "unspecified" where it asked a
	// yes-or-no question. A connector that cannot determine state fails the read.
	State IssueState
	// Title is the issue's title. It MAY be empty — a code host that does not
	// return one on the path a connector used is a vendor limit, not a failure,
	// the same position [CheckRun.DetailsURL] and [DiffFile.Patch] hold.
	//
	// ⚠️ A caller MUST NOT branch on Title. It is here to make a report to a
	// human legible, and an empty one is legitimate.
	Title string
}

// CodeCI is the connector-class contract.
//
// Every failure it returns is typed: a *cerr.Error whose Kind comes from
// cerr's closed vocabulary, so a host acts on the classification and never on
// the message. A failure the connector cannot classify is cerr.KindUnknown,
// which fails the call and escalates nothing.
//
// No list operation can report a success carrying no list — see [Resolution].
// No operation returns a pointer a caller must nil-check: a (*PR, nil) return
// is the same conflation in another shape, so the single-item reads return
// values.
//
// Optional operations live behind capabilities rather than in this
// interface: [CIController] behind [CapCIControl]. A host type-asserts for
// it, and the codeciconform harness fails a connector that declares it
// without implementing it — in both directions.
type CodeCI interface {
	connector.Connector

	// WhoAmI reports the account this connector's credential authenticates as
	// on the code host, as the code host itself answers — see [Identity].
	//
	// It MUST NOT return an [Identity] that is not [Identity.Coherent]: a
	// credential whose login cannot be learned is a typed failure
	// (cerr.KindUnauthorized when the code host rejected it,
	// cerr.KindUnavailable or cerr.KindTimeout when the read did not complete),
	// never a success carrying an empty login.
	//
	// It is a REQUIRED operation rather than one behind a capability. A host
	// publishes this answer as a standing assertion about how it authenticates,
	// and an assertion a host can only make for some connectors is not an
	// assertion — it is a fallback path, which is the per-host duplication
	// this class exists to remove. There is also no permission for a capability to
	// gate: a connector that can reach the code host authenticated at all,
	// which every operation above requires, can be told which account it
	// reached it as.
	WhoAmI(ctx context.Context) (Identity, error)

	// CreatePR opens the pull/merge request described by req, and returns it as
	// the host now holds it. It opens a DRAFT when req.Draft is set.
	//
	// It MUST call [CreatePRRequest.Validate] before doing anything else and
	// return its error unchanged: a request is a struct, so an unset required
	// field is silent at the call site, and the alternative to refusing it is
	// opening a pull/merge request titled "" — an artefact a human then has to
	// find and close.
	CreatePR(ctx context.Context, req CreatePRRequest) (PR, error)

	// ListPRs returns the pull/merge requests on a repository in the given
	// state, paging until the host reports no further page. A state that is
	// not usable as a filter is cerr.KindInvalid — see [PRStateUnspecified].
	//
	// It MUST page until the host reports no further pages. A repository with
	// more open PRs than one page holds is the ordinary case, and a caller
	// handed the first page and no error reads clean over a partial estate.
	ListPRs(ctx context.Context, fullName string, state PRState) (Resolution[PR], error)

	// MergePR merges prID using method.
	//
	// It MUST validate method before doing anything else: a method that is
	// not [MergeMethod.Specified] is refused with cerr.KindInvalid, and the
	// merge is not attempted. It MUST refuse to merge a pull/merge request
	// currently reported as a draft, with cerr.KindInvalid, before attempting
	// the merge — both guards are cheap and both prevent a merge that cannot
	// be undone.
	MergePR(ctx context.Context, fullName, prID string, method MergeMethod) error

	// GetPR returns prID as the host now holds it.
	//
	// It completes the change surface this class owns. Before 2026-08-12 a
	// caller could open, list, diff and merge through this class but had to
	// reach a DIFFERENT class to read one pull/merge request back — the code
	// host, which also carried open, list and comment under a second set of
	// names. Neither class was a subset of the other, so the split was a
	// decision to make rather than a duplication to tidy, and it was made in
	// this class's favour: a code host owns repositories and git, and a change
	// proposal is not either of those.
	//
	// ⚠️ An unresolvable prID is a typed FAILURE, never a zero PR. The zero
	// value is a syntactically valid pull/merge request whose number is 0 and
	// whose state is unset, and a caller that acts on it operates on nothing
	// while believing it read something.
	GetPR(ctx context.Context, fullName, prID string) (PR, error)

	// CommentPR posts body as a comment on prID and returns the identifier the
	// host assigned it.
	//
	// The returned identifier is what makes a comment addressable afterwards —
	// to be edited, resolved, or recognised as one this connector already
	// posted. A connector that discards it forces every caller to re-read the
	// whole thread to find its own comment, and to guess when two comments have
	// the same body.
	//
	// ⚠️ An empty body is refused with cerr.KindInvalid before anything is
	// posted. The alternative is an empty comment on someone's change, which is
	// an artefact a human then has to find and delete — the same reasoning
	// [CreatePR] applies to a request with no title.
	CommentPR(ctx context.Context, fullName, prID, body string) (commentID string, err error)

	// GetDiff returns every changed file in prID, paging until the host
	// reports no further page.
	//
	// A real pull/merge request always has at least one changed file, so
	// [EmptyList] is never a legitimate answer here — a connector that would
	// otherwise return one has a broken read, not an empty diff, and must
	// return a typed failure instead.
	GetDiff(ctx context.Context, fullName, prID string) (Resolution[DiffFile], error)

	// ListBranches returns every branch of the named repository, paging
	// until the host reports no further page.
	ListBranches(ctx context.Context, fullName string) (Resolution[Branch], error)

	// GetCheckRuns returns every check/status entry reported against ref.
	//
	// A ref with no CI configured against it is a genuine, expressible empty
	// result — see [EmptyList] — distinct from a read that failed.
	GetCheckRuns(ctx context.Context, fullName, ref string) (Resolution[CheckRun], error)

	// GetWorkflowRun returns one CI automation run. One that does not exist
	// is cerr.KindNotFound.
	GetWorkflowRun(ctx context.Context, fullName, runID string) (WorkflowRun, error)

	// GetIssue returns the issue numbered number in the named repository.
	//
	// ⚠️ It is BOARD-AGNOSTIC BY CONSTRUCTION, and that is the whole reason it
	// exists here: the address is a repository and a number, so an issue in a
	// repository whose issues are on NO board resolves exactly like any other.
	// There is no board argument to be missing and none to be guessed.
	//
	// ⚠️ An issue that does not exist is cerr.KindNotFound, and one the
	// credential cannot see is cerr.KindUnauthorized — a TYPED FAILURE, never a
	// value. A connector MUST NOT report an unresolvable issue as closed, as
	// open, or as a zero-valued Issue. The caller this serves is deciding
	// whether work is blocked, and a read that cannot be resolved must reach it
	// as unknown: silently reading it as closed tells an operator to start work
	// that is still blocked, which is the one failure direction here that does
	// damage rather than merely being wrong.
	//
	// A returned Issue MUST carry an [IssueState.Determined] State, and MUST
	// echo the fullName and number it was asked for.
	GetIssue(ctx context.Context, fullName string, number int) (Issue, error)

	// CloseIssue closes the issue numbered number in the named repository,
	// recording reason.
	//
	// It MUST validate reason before doing anything else: a reason that is not
	// [CloseReason.UsableAsReason] is refused with cerr.KindInvalid and nothing
	// is closed. Same shape as [CodeCI.MergePR]'s method guard, for the same
	// reason — a close, like a merge, is not conveniently undone.
	//
	// ⚠️ It MUST BE IDEMPOTENT. Closing an issue that is already closed is a
	// SUCCESS, not a failure: the caller's intent is "ensure this is closed",
	// and the postcondition holds. A connector that failed here would make a
	// caller's second pass over work it already finished report errors that
	// describe nothing wrong — and a caller cannot avoid a second pass, because
	// it cannot know whether its first one completed.
	//
	// ⚠️ Recording reason is BEST-EFFORT, and per-host. GitHub carries a native
	// close reason; GitLab's issue close takes no reason at all, so a GitLab
	// connector closes the issue and the distinction is not durably recorded on
	// the host. That is a vendor limit, and it MUST NOT fail the close: the
	// close is the postcondition the caller needs. A connector whose host cannot
	// record the reason MUST document that, and SHOULD record it where the host
	// does allow durable text.
	//
	// The reason is required from the CALLER regardless of whether the host can
	// store it, and this is deliberate: the argument makes the caller decide,
	// and a contract that accepted no reason on hosts that cannot store one
	// would let a caller stop deciding on all of them.
	//
	// An issue that does not exist is cerr.KindNotFound; one the credential
	// cannot close is cerr.KindUnauthorized.
	CloseIssue(ctx context.Context, fullName string, number int, reason CloseReason) error
}

// CIController is the optional contract operation behind [CapCIControl]:
// MUTATE a CI run rather than merely read its status.
//
// A connector that can do this does two things, and must do both: implement
// this interface, and declare [CapCIControl] in its manifest and from
// Capabilities(). Declaring without implementing is worse than declaring
// nothing — the host plans to retry a failed run and finds no operation to
// do it with.
type CIController interface {
	// RerunWorkflow re-runs runID.
	RerunWorkflow(ctx context.Context, fullName, runID string) error
	// CancelWorkflow cancels runID.
	CancelWorkflow(ctx context.Context, fullName, runID string) error
}
