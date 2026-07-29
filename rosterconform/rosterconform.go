// Package rosterconform checks a Roster connector against the parts of the
// contract a compiler cannot enforce.
//
// The Go type system already refuses several ways of getting this wrong: a
// connector that does not implement every list operation does not build, and a
// resolution carrying no list cannot be constructed as a success (see
// roster.Resolution). What is left are the properties that depend on BEHAVIOUR
// and on what the connector's manifest CLAIMS — and those need a harness.
//
// Run it in your own CI, against your own connector, before arqtos ever loads
// it:
//
//	rep, err := rosterconform.Run(ctx, myRoster, rosterconform.Options{
//		Manifest:            myManifest,
//		Group:               populatedGroupID,
//		AbsentGroup:         noSuchGroupID,
//		SuspendedPrincipal:  deactivatedPrincipalID,
//	})
//	if err != nil {
//		return err // the check could not be run at all
//	}
//	if err := rep.Err(); err != nil {
//		return err // the connector ran, and is not conformant
//	}
//
// # What it checks, and why each one exists
//
//   - [CheckManifest] — the manifest validates and declares only capabilities
//     this connector class defines.
//   - [CheckCapabilityHonesty] — what the manifest declares and what the
//     running connector reports are the same set.
//   - [CheckWatchDeclared] — the watch capability is declared exactly when
//     roster.Watcher is implemented, in both directions, AND — for a
//     connector that declares and implements it — that calling Watch actually
//     establishes one: a non-nil channel with no error, closing when its
//     context is cancelled. A Go type assertion proves the method exists; it
//     proves nothing about what calling it does.
//   - [CheckMachinePrincipalsDeclared] — machine principals are reported
//     exactly when the capability for them is declared, in both directions.
//   - [CheckTransitiveDeclared] — inherited memberships are reported exactly
//     when the capability for them is declared, in both directions.
//   - [CheckListsResolve] — every list operation comes back carrying a list —
//     never as a success carrying nothing, and never as a success asserted
//     [roster.Partial] — and the principal list AND the group list carry
//     ACTUAL ENTRIES rather than an assertion that the directory is empty.
//   - [CheckSuspendedIsPresent] — a deactivated principal is reported, with
//     Active false, rather than omitted.
//   - [CheckMembershipShape] — every membership is for the group that was
//     asked about.
//   - [CheckFailureTyped] — a group that does not exist fails with a
//     classified error from the closed vocabulary, so a host never parses
//     vendor prose.
//
// # Presence is not substance
//
// Three obligations here deliberately demand more than "it did not error",
// because the weaker form is what a broken connector passes.
//
// [CheckListsResolve] requires the principal list to hold principals AND the
// group list to hold groups: a connector that answers every read with
// roster.EmptyRoster() is present, readable and completely empty, and under a
// presence-only check it scores fully green while telling the host that
// nobody works here and nothing is grouped.
//
// Requiring groups specifically does not create a false positive for a
// directory that genuinely has none — but not because [Run] verifies
// Options.Group names a real or populated group. It does not, and cannot,
// from a string alone: [Run] refuses only an EMPTY one (see [Options.Group]).
// What actually rules it out is what a connector fed a nonexistent
// Options.Group hits first: ListMemberships for a group that does not exist
// must fail typed (cerr.KindNotFound, never an empty roster — see
// [roster.Roster.ListMemberships]), which fails THIS check at the
// ListMemberships stage, above, before the group-substance requirement below
// is ever reached, and fails [CheckMembershipShape] the same way. A connector
// that instead answers the nonexistent group with a readable-but-empty
// membership list is still caught, by [CheckMembershipShape]'s separate
// requirement that the nominated group have at least one member. Both of
// those held before [CheckMembershipShape] was given one more job: it also
// correlates Options.Group against the groups [roster.Roster.ListGroups]
// itself reported, so a connector that fabricates correctly-shaped members
// for a group id its own ListGroups never claims exists is refused BY NAME
// rather than by the coincidence of the first two mechanisms lining up. That
// correlation is what makes the no-false-positive property hold BY
// CONSTRUCTION; the two failure paths above only made it hold incidentally.
//
// [CheckSuspendedIsPresent] requires a named deactivated principal to
// actually be in the list, because "the list has entries" says nothing about
// whether suspended people survived the read.
//
// [CheckWatchDeclared] requires Watch to actually establish a subscription
// when the capability is declared and the method exists. A connector whose
// Watch always answers (nil, cerr.KindUnsupported) — the stub an author
// reaches for while a real subscription is still unimplemented — or (nil
// channel, nil error) satisfies a bare type assertion and would otherwise
// score green: the first tells a host expecting push notification nothing at
// all while it waits forever believing an event will arrive, and the second
// is worse — a host that ranges over a nil channel blocks forever and never
// reconciles again.
//
// # A truncated read cannot become a success
//
// roster.Resolved requires a roster.Completeness on every call, and refuses
// to build a readable Resolution when it is asserted roster.Partial (see
// [roster.Completeness]) — the same way it refuses an empty list. A
// connector that reports a truncated read that way returns a *roster.FaultError,
// which [CheckListsResolve] and [CheckMembershipShape] treat exactly as they
// already treat any other resolution failure: not readable, not green. This
// package adds no separate check for it, because none is needed — the
// type-level fix is what closes the hole a check could only have described
// after the fact. What no check anywhere can catch is a connector that
// asserts roster.Complete on a list it knows to be truncated; that is a
// deliberate misclassification at the call site, not a shape a harness can
// distinguish from the truth from the outside.
//
// # The declaration checks are not tautological, and the tests prove it
//
// A declared-is-implemented check is worthless if "implemented" is computed
// from the same signal as "declared" — the check then agrees with itself
// whatever the connector does. That has happened in this SDK before, in the
// credential class's harness over an out-of-process provider, where the
// host-side stub's shape was derived from the very declaration the check was
// verifying.
//
// It cannot happen here, for two reasons, and both are load-bearing rather
// than incidental:
//
//   - "Implements roster.Watcher" is a Go type assertion against the
//     connector's own type, which no declaration can influence. That holds
//     across the Track-B boundary too, and by construction rather than by
//     luck: watch has no RPC on that wire (see plugin/roster.go), so the
//     dispensed host stub never satisfies roster.Watcher whatever the provider
//     declares. The consequence for a provider author is real and is stated
//     there — an out-of-process Roster connector cannot declare watch, and is
//     polled.
//   - The two behavioural capabilities are judged from the DATA the connector
//     returned — a principal whose Kind is machine, a membership whose Direct
//     is false — and data is not derived from a manifest either. This is what
//     keeps them independent over the wire, where the host stub DOES read the
//     capability RPC (it needs the declaration to apply the host-side guards):
//     the declaration reaches the guards, never the judgement.
//
// The tests drive each of these checks through all four combinations of
// (declared, actually the case) and pin the verdict of each. A tautological
// check can only produce two distinct verdicts across those four inputs, so
// the truth table is what proves the check independent rather than a comment
// asserting it. Whoever gives watch an RPC must NOT derive the stub's
// Watcher-ness from the capability RPC, or that proof silently stops meaning
// anything — probe the provider's behaviour instead.
//
// # In-process is not the same run
//
// [Run] takes a roster.Roster, so it works unchanged against a host stub
// talking to a subprocess — but it cannot TELL, and a report from it records
// [TransportUnrecorded] rather than claiming the wire was exercised.
// [RunOutOfProcess] spawns a provider binary and runs every check across a
// real gRPC boundary. A connector shipped as an out-of-process provider needs
// that one: the entire class of marshalling bugs — an unresolved read arriving
// as an empty directory, a suspended principal losing its flag, a membership
// arriving for a group nobody asked about — is invisible to a run that never
// serialised anything.
//
// # The harness is driven by non-compliant connectors too
//
// Every check in this package has a test that drives it with a connector
// deliberately built to violate the property it checks — one that reports an
// unresolved list as a success, one that drops suspended people, one whose
// manifest declares a watch it does not implement, one that returns
// memberships for the wrong group. A harness only ever run against compliant
// input proves nothing about what it would catch.
package rosterconform

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// watchCloseWait bounds how long checkWatchDeclared waits for a Watch
// channel to close after its context is cancelled. It exists to keep a
// misbehaving connector from hanging the harness forever, not to assert
// anything about how FAST a well-behaved one closes its channel — a
// generous upper bound, not a tight one.
const watchCloseWait = 2 * time.Second

// Check names reported by [Run]. They are stable identifiers: a caller may
// switch on them, and a CI job may allowlist a known failure by name.
const (
	// CheckManifest covers the connector's manifest validating, declaring
	// this class, and declaring only capabilities the class defines.
	CheckManifest = "manifest/valid"
	// CheckCapabilityHonesty covers the manifest's declared capabilities and
	// the running connector's Capabilities() being the same set.
	CheckCapabilityHonesty = "capability/manifest-matches-runtime"
	// CheckWatchDeclared covers the watch capability being declared exactly
	// when roster.Watcher is implemented.
	//
	// "Implemented" is a Go interface assertion against the value Run
	// received, so it is independent of everything the connector declares.
	// That independence is the check's whole value and it is verified by a
	// four-case truth table in this package's tests, not assumed.
	CheckWatchDeclared = "watch/declared-is-implemented"
	// CheckMachinePrincipalsDeclared covers machine principals appearing in
	// the principal list exactly when the capability for them is declared.
	//
	// Undeclared-but-reported is a contract violation the host-side guard
	// also refuses. Declared-but-never-reported is a failure of this run, and
	// the fixture directory is what has to answer it: a connector that claims
	// it can see service identities must be pointed at a directory that has
	// one, or it is claiming something this run cannot distinguish from a
	// connector that cannot see them at all.
	CheckMachinePrincipalsDeclared = "machine-principals/declared-is-reported"
	// CheckTransitiveDeclared covers inherited memberships appearing exactly
	// when the transitive-membership capability is declared, in both
	// directions, for the same reasons.
	CheckTransitiveDeclared = "transitive-membership/declared-is-reported"
	// CheckListsResolve covers each list operation returning a READABLE
	// resolution, and the principal list carrying actual principals rather
	// than an assertion that the directory is empty.
	CheckListsResolve = "lists/resolve-not-empty"
	// CheckSuspendedIsPresent covers a deactivated principal being reported
	// with Active false rather than omitted from the list.
	CheckSuspendedIsPresent = "principals/suspended-is-present-not-absent"
	// CheckMembershipShape covers every returned membership being for the
	// group that was requested.
	CheckMembershipShape = "memberships/match-requested-group"
	// CheckFailureTyped covers a group that does not exist failing with a
	// classified error from the closed vocabulary.
	CheckFailureTyped = "failure/typed"
)

// Options are the fixtures a conformance run needs. Every field is required: a
// check that cannot be driven is not skipped, because a report that is green
// because nothing looked is the failure this package exists to avoid.
type Options struct {
	// Manifest is the connector.yml this connector ships. It is what an
	// external author encodes and what a host reads, so the run compares it
	// against the running connector rather than trusting either alone.
	Manifest manifest.Doc

	// Group is the id of a POPULATED group this connector must be able to list
	// the memberships of. It drives the membership shape check and the
	// transitive-membership check.
	//
	// It must have at least one member. An empty group is a real state and a
	// conformant connector reports one, but a membership list with nothing in
	// it proves nothing about whether what came back corresponds to what was
	// asked — and a check that could not be driven is not a check that passed.
	//
	// Where the connector declares transitive membership, point this at a
	// group that also has an INHERITED member: that is the only way the run
	// can tell a connector which resolves nesting from one that merely says
	// it does.
	Group string

	// AbsentGroup is the id of a group this connector MUST NOT find: absent
	// from the directory, or outside what this connector serves. It drives the
	// typed-failure check, which cannot be run against a connector that never
	// fails.
	//
	// It must not be an EMPTY group. "This group has no members" and "there is
	// no such group" are different answers, and only the second one is a
	// failure.
	AbsentGroup string

	// SuspendedPrincipal is the stable directory id of a principal the fixture
	// directory holds in a DEACTIVATED state.
	//
	// It is required because the failure it guards against is the most
	// destructive one this contract has: a connector that omits deactivated
	// identities tells the host they left the organisation, and the host
	// revokes everything belonging to somebody who is on leave. A run without
	// a suspended principal cannot see that, so the run refuses rather than
	// score green on a property it never examined.
	SuspendedPrincipal string
}

// A Result is the outcome of a single named check.
type Result struct {
	// Name is one of the Check* constants.
	Name string
	// Pass reports whether the check succeeded.
	Pass bool
	// Detail explains the outcome. It is always populated for a failure and
	// may be empty for a pass.
	Detail string
}

// A Report is the outcome of a conformance run.
type Report struct {
	// Connector is the name the manifest gives the connector under test, so a
	// failure is attributable without cross-referencing the run.
	Connector string
	// Transport records HOW the connector under test was reached, because a
	// green report does not mean the same thing in both cases.
	//
	// [Run] takes a roster.Roster and cannot tell a natively-compiled
	// connector from a host stub talking to a subprocess — so it records
	// [TransportUnrecorded] rather than guessing, and a report carrying that
	// is not evidence the wire was exercised. [RunOutOfProcess] knows, and
	// says so.
	//
	// The distinction is the same "green because nothing looked" failure this
	// package's own checks are built around, one level up: an out-of-process
	// connector whose conformance was only ever proved in-process has had the
	// entire class of wire bugs go unexamined.
	Transport string
	// Results holds one entry per check that was run, in run order.
	Results []Result
}

// Transport values for [Report.Transport].
const (
	// TransportUnrecorded is what [Run] records: it was handed a
	// roster.Roster and cannot know what is behind it.
	TransportUnrecorded = "transport not recorded"
	// TransportOutOfProcess is what [RunOutOfProcess] records — the
	// connector ran as a separate process and every check crossed a real
	// gRPC boundary.
	TransportOutOfProcess = "out-of-process"
)

// OK reports whether every check that ran passed.
func (r Report) OK() bool { return len(r.Failures()) == 0 }

// Failures returns the failed checks, in run order.
func (r Report) Failures() []Result {
	var out []Result
	for _, res := range r.Results {
		if !res.Pass {
			out = append(out, res)
		}
	}
	return out
}

// Err returns nil when the run passed, and otherwise a cerr of kind
// [cerr.KindInvalid] naming the connector and every failed check. The connector
// ran; it is its behaviour that is wrong, which is why this is Invalid rather
// than Unavailable.
func (r Report) Err() error {
	failed := r.Failures()
	if len(failed) == 0 {
		return nil
	}
	parts := make([]string, 0, len(failed))
	for _, f := range failed {
		parts = append(parts, fmt.Sprintf("%s: %s", f.Name, f.Detail))
	}
	return cerr.New(cerr.KindInvalid, "rosterconform", fmt.Errorf("connector %q: %s", r.connectorName(), strings.Join(parts, "; ")))
}

// String renders the report as one line per check, for CI logs.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "rosterconform: connector=%s transport=%s", r.connectorName(), r.transportName())
	for _, res := range r.Results {
		status := "PASS"
		if !res.Pass {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "\n  %s %s", status, res.Name)
		if res.Detail != "" {
			fmt.Fprintf(&b, ": %s", res.Detail)
		}
	}
	return b.String()
}

func (r Report) transportName() string {
	if r.Transport == "" {
		return TransportUnrecorded
	}
	return r.Transport
}

func (r Report) connectorName() string {
	if r.Connector == "" {
		return "<unnamed>"
	}
	return r.Connector
}

func (r *Report) add(name string, pass bool, detail string) {
	r.Results = append(r.Results, Result{Name: name, Pass: pass, Detail: detail})
}

// Run checks c against the contract obligations this package covers.
//
// The returned error is non-nil only when the run could not be carried out —
// no connector, or missing fixtures. A connector that runs and is
// non-conformant yields a nil error and a Report whose Err reports the
// failures; gate on Report.Err, not on the returned error alone.
func Run(ctx context.Context, c roster.Roster, opts Options) (Report, error) {
	if c == nil {
		return Report{}, cerr.New(cerr.KindInvalid, "rosterconform.Run", fmt.Errorf("nil connector"))
	}
	for _, missing := range []struct{ field, value, why string }{
		{"Group", opts.Group, "without a group the connector must find, neither its membership listing nor the " +
			"correspondence between what was asked and what came back is ever exercised"},
		{"AbsentGroup", opts.AbsentGroup, "without a group the connector must fail on, its failure classification " +
			"is never exercised"},
		{"SuspendedPrincipal", opts.SuspendedPrincipal, "without a deactivated principal, the run cannot tell a " +
			"connector that reports suspended identities from one that drops them — and dropping them is what makes " +
			"a host revoke access for someone who is merely on leave"},
	} {
		if missing.value == "" {
			return Report{}, cerr.New(cerr.KindInvalid, "rosterconform.Run", fmt.Errorf(
				"fixture Options.%s is unset: %s", missing.field, missing.why))
		}
	}

	rep := Report{Connector: opts.Manifest.Name, Transport: TransportUnrecorded}
	runtime := c.Capabilities()

	checkManifest(&rep, opts.Manifest)
	checkCapabilityHonesty(&rep, runtime, opts.Manifest)

	watcher, isWatcher := c.(roster.Watcher)
	checkWatchDeclared(ctx, &rep, opts.Manifest, runtime, watcher, isWatcher)

	// The lists are read ONCE and every remaining check reads that one
	// snapshot. Re-reading per check would let a connector answer differently
	// each time and still score green, which is the same class of hole as a
	// check that only asks about presence.
	r := readAll(ctx, c, opts, runtime)

	checkListsResolve(&rep, r)
	checkMembershipShape(&rep, opts, r)
	checkMachinePrincipalsDeclared(&rep, opts.Manifest, runtime, r)
	checkTransitiveDeclared(&rep, opts.Manifest, runtime, r)
	checkSuspendedIsPresent(&rep, opts, r)
	checkFailureTyped(ctx, &rep, c, opts)

	return rep, nil
}

// reads is one snapshot of what the connector returned, together with what the
// HOST-SIDE GUARDS made of it.
//
// The resolution error and the guard error are kept apart on purpose. Both come
// from the same single call, but they answer different questions — "did this
// resolve at all" and "is everything in it something this connector is allowed
// to report" — and collapsing them would make one broken property fail the
// check named for another. An author reading a report has to be able to go
// straight to the thing that is wrong.
type reads struct {
	// groups is what ListGroups resolved to, and groupsErr is its resolution
	// failure. Exactly one of the two is meaningful.
	groups    []roster.Group
	groupsErr error
	// principals is what ListPrincipals resolved to, and principalsErr is its
	// resolution failure. Exactly one of the two is meaningful.
	principals    []roster.Principal
	principalsErr error
	// principalsGuardErr is what the full host-side guard made of the same
	// return — which additionally refuses a machine principal from a connector
	// that has not declared it can see one.
	principalsGuardErr error
	// memberships, membershipsErr and membershipsGuardErr are the same three
	// things for the nominated group's membership list.
	memberships         []roster.Membership
	membershipsErr      error
	membershipsGuardErr error
}

// readAll performs every read the run needs, exactly once each, and runs both
// the resolution guard and the full host-side guard over each return.
func readAll(ctx context.Context, c roster.Roster, opts Options, runtime connector.Capabilities) reads {
	name := opts.Manifest.Name
	var r reads

	groupRes, groupErr := c.ListGroups(ctx)
	if checked, err := roster.CheckResolution(name, "ListGroups", groupRes, groupErr); err != nil {
		r.groupsErr = err
	} else {
		r.groups, r.groupsErr = checked.Items()
	}

	principalRes, principalErr := c.ListPrincipals(ctx)
	if checked, err := roster.CheckResolution(name, "ListPrincipals", principalRes, principalErr); err != nil {
		r.principalsErr = err
	} else {
		r.principals, r.principalsErr = checked.Items()
	}
	_, r.principalsGuardErr = roster.CheckPrincipals(name, runtime, principalRes, principalErr)

	memberRes, memberErr := c.ListMemberships(ctx, opts.Group)
	if checked, err := roster.CheckResolution(name, "ListMemberships", memberRes, memberErr); err != nil {
		r.membershipsErr = err
	} else {
		r.memberships, r.membershipsErr = checked.Items()
	}
	_, r.membershipsGuardErr = roster.CheckMemberships(name, opts.Group, runtime, memberRes, memberErr)

	return r
}

// checkListsResolve requires all three list operations to come back READABLE
// — never a success carrying nothing, and never a success asserted
// [roster.Partial] rather than [roster.Complete] (both surface as the same
// *roster.FaultError, via [roster.CheckResolution]) — and requires the
// principal list AND the group list to carry actual entries.
//
// Substance rather than presence, and the gap is not theoretical. A connector
// can answer every read with roster.EmptyRoster() — present, readable, nothing
// in it — and that is exactly the move an author reaches for when
// roster.Resolved refuses their failing backend: it makes the error go away
// without making the directory readable. Checked for presence only, such a
// connector scores a fully green report while telling the host that everybody
// left and nothing is grouped.
//
// Requiring groups specifically cannot register a false positive for a
// directory that genuinely has none — not because [Run] verifies
// Options.Group names a real or populated group (it verifies only that the
// string is non-empty), but because a connector fed a nonexistent
// Options.Group fails earlier: ListMemberships for a group that does not
// exist must fail typed, which trips the stage loop just below before this
// group-count branch is ever reached, and trips [CheckMembershipShape] too. A
// connector that answers the nonexistent group with a readable-but-empty list
// instead is still caught by [CheckMembershipShape]'s own requirement that the
// nominated group have at least one member. [CheckMembershipShape] also
// correlates Options.Group against what [roster.Roster.ListGroups] itself
// reported, which is what makes "Options.Group names a real group" hold by
// construction rather than by the coincidence of those two failure paths.
func checkListsResolve(rep *Report, r reads) {
	for _, stage := range []struct {
		op  string
		err error
	}{
		{"ListGroups", r.groupsErr},
		{"ListPrincipals", r.principalsErr},
		{"ListMemberships", r.membershipsErr},
	} {
		if stage.err != nil {
			rep.add(CheckListsResolve, false, listFailureDetail(stage.op, stage.err))
			return
		}
	}
	if len(r.principals) == 0 {
		rep.add(CheckListsResolve, false,
			"ListPrincipals resolved to NO PRINCIPALS. roster.EmptyRoster asserts that a directory genuinely holds "+
				"nobody; it is not an answer for a run whose whole purpose is to prove this connector reads a "+
				"directory, and a connector that answers every list that way tells the host that everybody left "+
				"while passing a presence-only check. Point the run at a populated directory, or fix the read")
		return
	}
	if len(r.groups) == 0 {
		rep.add(CheckListsResolve, false,
			"ListGroups resolved to NO GROUPS. roster.EmptyRoster asserts that a directory genuinely has no groups at "+
				"all; it is not an answer for a run whose Options.Group fixture names a group this connector must be "+
				"able to list — a run that requires a populated group already presupposes at least one exists. Point "+
				"ListGroups at a directory that includes it, or fix the read")
		return
	}
	rep.add(CheckListsResolve, true, fmt.Sprintf("%d principal(s), %d group(s); memberships resolved", len(r.principals), len(r.groups)))
}

// checkMembershipShape requires every returned membership to be for the group
// that was asked about, requires there to be at least one, and requires
// Options.Group itself to be a group [roster.Roster.ListGroups] actually
// reported.
//
// The third requirement is the one that makes "Options.Group names a real,
// populated group" hold BY CONSTRUCTION rather than by accident. Without it, a
// connector whose ListMemberships answers with correctly-shaped,
// correctly-attributed members for a group id its own ListGroups never lists
// is fully green: the membership list is real, every entry names the group
// that was asked about, and nothing about SHAPE alone catches a group that
// does not otherwise exist. Correlating against ListGroups closes that,
// cheaply, with the one list this package already reads.
func checkMembershipShape(rep *Report, opts Options, r reads) {
	if r.membershipsErr != nil {
		rep.add(CheckMembershipShape, false, fmt.Sprintf(
			"listing the memberships of group %q, which this run nominates as one the connector must read: %v",
			opts.Group, r.membershipsErr))
		return
	}
	var fe *roster.FaultError
	if errors.As(r.membershipsGuardErr, &fe) && fe.Fault == roster.FaultMembershipMismatch {
		rep.add(CheckMembershipShape, false, fmt.Sprintf(
			"%v. A host cannot attribute a membership for a group it did not ask about, and attributing it to the "+
				"group it did ask about puts people in groups they are not in", fe))
		return
	}
	if len(r.memberships) == 0 {
		rep.add(CheckMembershipShape, false, fmt.Sprintf(
			"group %q has no memberships, so nothing about the correspondence between what was asked and what came "+
				"back was exercised. Nominate a POPULATED group: an empty group is a real state, but it is not a test",
			opts.Group))
		return
	}
	// r.groupsErr == nil: when ListGroups itself failed to resolve, that is
	// already CheckListsResolve's failure to report, and there is no group
	// list here to correlate against at all.
	if r.groupsErr == nil && !groupIsListed(r.groups, opts.Group) {
		rep.add(CheckMembershipShape, false, fmt.Sprintf(
			"group %q, which Options.Group nominates as the group this connector must be able to list the "+
				"memberships of, is not among the %d group(s) ListGroups reported. Every returned membership names "+
				"the right group, but a membership list cannot vouch for the existence of the group it is for — "+
				"nominate a group ListGroups actually returns, or fix ListGroups to report it",
			opts.Group, len(r.groups)))
		return
	}
	rep.add(CheckMembershipShape, true, fmt.Sprintf("%d membership(s), all for the requested group", len(r.memberships)))
}

// groupIsListed reports whether id is one of groups' IDs.
func groupIsListed(groups []roster.Group, id string) bool {
	for _, g := range groups {
		if g.ID == id {
			return true
		}
	}
	return false
}

func checkManifest(rep *Report, m manifest.Doc) {
	if err := m.Validate(); err != nil {
		rep.add(CheckManifest, false, err.Error())
		return
	}
	if m.Implements != connector.ClassRoster {
		rep.add(CheckManifest, false, fmt.Sprintf(
			"manifest implements %q; this harness checks the %q class",
			m.Implements, connector.ClassRoster))
		return
	}
	// Capability-vocabulary closure is not re-implemented here.
	// manifest.Doc.Validate, called above, rejects a capability outside the
	// vocabulary of the class the manifest implements — which is where the
	// check belongs, because a host runs Validate before it loads anything,
	// with no connector and no fixtures. Duplicating it in the harness would
	// be a second copy of a closed vocabulary, free to drift from the first.
	rep.add(CheckManifest, true, "")
}

func checkCapabilityHonesty(rep *Report, runtime connector.Capabilities, m manifest.Doc) {
	declared := connector.Capabilities(m.Capabilities)

	var missing, undeclared []string
	for _, c := range declared {
		if !runtime.Has(c) {
			missing = append(missing, string(c))
		}
	}
	for _, c := range runtime {
		if !declared.Has(c) {
			undeclared = append(undeclared, string(c))
		}
	}
	switch {
	case len(missing) > 0 && len(undeclared) > 0:
		rep.add(CheckCapabilityHonesty, false, fmt.Sprintf(
			"manifest declares %s which Capabilities() does not report, and Capabilities() reports %s which the manifest does not declare",
			strings.Join(missing, ", "), strings.Join(undeclared, ", ")))
	case len(missing) > 0:
		rep.add(CheckCapabilityHonesty, false, fmt.Sprintf(
			"manifest declares %s, which the running connector does not report. A host plans for what the manifest promises",
			strings.Join(missing, ", ")))
	case len(undeclared) > 0:
		rep.add(CheckCapabilityHonesty, false, fmt.Sprintf(
			"the running connector reports %s, which the manifest does not declare. The manifest is what a host reads before it ever loads the connector",
			strings.Join(undeclared, ", ")))
	default:
		rep.add(CheckCapabilityHonesty, true, "")
	}
}

// checkWatchDeclared fails in BOTH structural directions, because both are a
// manifest that does not describe the connector — and, for a connector that
// passes both, ALSO requires that calling Watch actually establishes a
// subscription.
//
// Declared-but-absent is the dangerous structural failure: a host that
// believes it will be pushed membership change lengthens its own reconcile
// interval, and then is never told. Implemented-but-undeclared is the
// wasteful one: the host polls a directory that would have told it, on every
// interval, forever.
//
// implemented is a Go interface assertion at the CALL SITE, made against
// whatever value Run received — the connector's own type, independent of its
// manifest and of its Capabilities() alike. That independence is what makes
// the structural half of this check mean anything, and it is why this class
// stays in-process for now: a host-side stub that synthesised its
// Watcher-ness from a capability RPC would collapse "implemented" and
// "declared" into one signal, and this function would then agree with itself
// no matter what the connector's backend could actually do.
//
// But a type assertion is presence, not substance: it proves the method
// exists, and nothing about what calling it does. A connector whose Watch
// always answers (nil, cerr.KindUnsupported) — the stub an author reaches for
// while a real subscription is still unimplemented — or (nil channel, nil
// error) passes that assertion and would otherwise score green here. So a
// connector that is declared and implemented is also required to actually
// establish a watch: [establishesWatch] calls Watch with a cancellable
// context, requires a non-nil channel with no error, PROVES with a
// non-blocking receive taken before cancellation that the channel was not
// already closed, then cancels and requires the channel to close. Cancelling
// first and only then checking for closure cannot tell that apart from a
// channel that was already closed the moment Watch returned —
// ch := make(chan Change); close(ch); return ch, nil passes a cancel-then-wait
// check while establishing no subscription at all — which is why the
// already-closed observation has to happen first.
func checkWatchDeclared(ctx context.Context, rep *Report, m manifest.Doc, runtime connector.Capabilities, w roster.Watcher, implemented bool) {
	inManifest := m.Declares(roster.CapWatch)
	atRuntime := runtime.Has(roster.CapWatch)

	switch {
	case (inManifest || atRuntime) && !implemented:
		rep.add(CheckWatchDeclared, false, fmt.Sprintf(
			"%s is declared %s, but the connector does not implement roster.Watcher. "+
				"A declared capability that is absent is worse than an undeclared one: a host that expects to be told "+
				"about membership change waits to be told, and is not",
			roster.CapWatch, declaredIn(inManifest, atRuntime)))
	case implemented && !(inManifest && atRuntime):
		rep.add(CheckWatchDeclared, false, fmt.Sprintf(
			"the connector implements roster.Watcher, but %s is declared %s. "+
				"A host reads the declaration before it calls anything, so a watch that is not declared in both places "+
				"is never subscribed to and every reconcile is a full poll",
			roster.CapWatch, declaredIn(inManifest, atRuntime)))
	case implemented:
		observed, ok := establishesWatch(ctx, w)
		if !ok {
			rep.add(CheckWatchDeclared, false, observed)
			return
		}
		rep.add(CheckWatchDeclared, true, fmt.Sprintf(
			"declared in the manifest and by Capabilities(), implemented, and %s", observed))
	default:
		rep.add(CheckWatchDeclared, true, "not declared, not implemented")
	}
}

// establishesWatch calls w.Watch, rather than merely trusting that the method
// exists: a Go type assertion says nothing about whether the call succeeds,
// returns a usable channel, or honours ctx.
//
// It requires, in order: no error establishing the watch; a non-nil channel (a
// host that ranges over a nil channel blocks forever); that a non-blocking
// receive taken BEFORE ctx is cancelled does not observe the channel already
// closed; and — after the context is cancelled — that the channel closes
// within [watchCloseWait].
//
// The open-before-cancel step exists because cancelling first and only then
// watching for a close cannot distinguish "closes because ctx was cancelled"
// from "was already closed, and was always going to read that way" — both
// shapes end in an observed close after cancel(), because a close that
// already happened stays observed. The most idiomatic-looking of the stub
// Watches this check exists to catch —
// ch := make(chan Change); close(ch); return ch, nil — passed a
// cancel-then-wait-for-close check for exactly that reason: it establishes
// nothing, yet "closes on cancellation" is trivially true of a channel that
// was never open to begin with. A non-blocking receive never blocks on a
// closed channel (it is always immediately ready), so taking one before
// cancelling separates the two: a channel that is still open takes the
// default branch (or delivers a queued hint), and a channel that was already
// closed takes the receive branch with open=false, right there, before ctx is
// ever touched.
//
// The [watchCloseWait] wait is a generous upper bound guarding the harness
// against a connector that never closes its channel, not an assertion that a
// well-behaved one closes quickly; it never fires for the reference behaviour
// this contract documents, which closes promptly on cancellation.
func establishesWatch(ctx context.Context, w roster.Watcher) (detail string, ok bool) {
	wctx, cancel := context.WithCancel(ctx)
	ch, err := w.Watch(wctx)
	if err != nil {
		cancel()
		return fmt.Sprintf(
			"Watch(ctx) returned an error establishing the watch: %v. A connector that declares %s must be able to "+
				"establish one; if it cannot, it must not declare the capability", err, roster.CapWatch), false
	}
	if ch == nil {
		cancel()
		return "Watch(ctx) returned a nil channel and a nil error. A host that ranges over a nil channel blocks " +
			"forever and never reconciles again", false
	}

	// Prove the channel is open BEFORE cancelling — see the doc comment. A
	// non-blocking receive on a closed channel is always immediately ready, so
	// this is what tells "closes on cancellation" apart from "was already
	// closed".
	select {
	case _, open := <-ch:
		if !open {
			cancel()
			return "Watch(ctx) returned a channel that was already closed, before ctx was ever cancelled. A channel " +
				"closed from the start establishes no subscription: a host that ranges over it sees end-of-channel " +
				"immediately and never receives a single hint, which is indistinguishable from a connector that was " +
				"never watching anything at all", false
		}
		// A hint delivered before we even cancel is a live, open channel;
		// fall through to the cancellation half of the contract below.
	default:
		// Nothing queued yet — the ordinary shape of a fresh subscription
		// with nothing to report. Also fall through.
	}

	cancel() // ctx is done; the contract requires the channel to close.
	deadline := time.NewTimer(watchCloseWait)
	defer deadline.Stop()
	for {
		select {
		case _, open := <-ch:
			if !open {
				return fmt.Sprintf(
					"Watch's channel was open before ctx was cancelled, and closed within %s of cancellation",
					watchCloseWait), true
			}
			// A hint delivered as we cancel is fine; keep draining for close.
		case <-deadline.C:
			return "the channel Watch(ctx) returned did not close within a reasonable time of ctx being cancelled. A " +
				"host that never sees this channel close cannot tell a lost watch from one still live, and never " +
				"falls back to its poll", false
		}
	}
}

// checkMachinePrincipalsDeclared fails in both directions, and neither
// direction is read from the declaration:
//
//   - Declared, and no machine principal in the list: the claim is
//     unsubstantiated. A host that plans differently for service identities
//     gets nothing to plan with, and this run cannot distinguish the claim
//     from a connector that cannot see them at all.
//   - A machine principal in the list, and not declared: the host reads the
//     absence of machine principals as a fact about the DIRECTORY only when
//     the connector has said it can see them. An undeclared one makes that
//     reading wrong for every other connector too.
//
// "Actually the case" comes from the DATA the connector returned, which is why
// this cannot degenerate into the check agreeing with itself.
func checkMachinePrincipalsDeclared(rep *Report, m manifest.Doc, runtime connector.Capabilities, r reads) {
	if r.principalsErr != nil {
		rep.add(CheckMachinePrincipalsDeclared, false,
			"the principal list could not be read, so this check could not be driven — see "+CheckListsResolve)
		return
	}
	declared := m.Declares(roster.CapMachinePrincipals) || runtime.Has(roster.CapMachinePrincipals)

	machine := 0
	for _, p := range r.principals {
		if p.Kind == roster.PrincipalMachine {
			machine++
		}
	}
	switch {
	case declared && machine == 0:
		rep.add(CheckMachinePrincipalsDeclared, false, fmt.Sprintf(
			"%s is declared %s, and not one of the %d principal(s) read has Kind %s. Either the fixture directory "+
				"has no machine identity — in which case point the run at one that does, because an undriven check is "+
				"not a passing check — or the connector cannot in fact see them and must stop declaring that it can",
			roster.CapMachinePrincipals, declaredIn(m.Declares(roster.CapMachinePrincipals), runtime.Has(roster.CapMachinePrincipals)),
			len(r.principals), roster.PrincipalMachine))
	case !declared && machine > 0:
		rep.add(CheckMachinePrincipalsDeclared, false, fmt.Sprintf(
			"%d principal(s) have Kind %s, and %s is declared nowhere (the host-side guard refuses this too: %v). "+
				"A host reads the absence of machine principals as a fact about the directory only when the connector "+
				"has declared it can see them",
			machine, roster.PrincipalMachine, roster.CapMachinePrincipals, r.principalsGuardErr))
	case declared:
		rep.add(CheckMachinePrincipalsDeclared, true, fmt.Sprintf("declared, and %d machine principal(s) reported", machine))
	default:
		rep.add(CheckMachinePrincipalsDeclared, true, "not declared, and none reported")
	}
}

// checkTransitiveDeclared is [checkMachinePrincipalsDeclared]'s sibling for
// group nesting, and fails in both directions for the same reasons. An
// inherited membership is one with Direct false, and that too is read from the
// data rather than from any declaration.
func checkTransitiveDeclared(rep *Report, m manifest.Doc, runtime connector.Capabilities, r reads) {
	if r.membershipsErr != nil {
		rep.add(CheckTransitiveDeclared, false,
			"the membership list could not be read, so this check could not be driven — see "+CheckMembershipShape)
		return
	}
	declared := m.Declares(roster.CapTransitiveMembership) || runtime.Has(roster.CapTransitiveMembership)

	inherited := 0
	for _, mem := range r.memberships {
		if !mem.Direct {
			inherited++
		}
	}
	switch {
	case declared && inherited == 0:
		rep.add(CheckTransitiveDeclared, false, fmt.Sprintf(
			"%s is declared %s, and not one of the %d membership(s) of the nominated group is inherited. Either the "+
				"group has no nested member — in which case nominate one that does, because an undriven check is not a "+
				"passing check — or the connector cannot in fact resolve nesting and must stop declaring that it can",
			roster.CapTransitiveMembership,
			declaredIn(m.Declares(roster.CapTransitiveMembership), runtime.Has(roster.CapTransitiveMembership)),
			len(r.memberships)))
	case !declared && inherited > 0:
		rep.add(CheckTransitiveDeclared, false, fmt.Sprintf(
			"%d membership(s) are inherited, and %s is declared nowhere (the host-side guard refuses this too: %v). "+
				"A host reads a flat membership list as a fact about the directory only when the connector has "+
				"declared it can express nesting",
			inherited, roster.CapTransitiveMembership, r.membershipsGuardErr))
	case declared:
		rep.add(CheckTransitiveDeclared, true, fmt.Sprintf("declared, and %d inherited membership(s) reported", inherited))
	default:
		rep.add(CheckTransitiveDeclared, true, "not declared, and none reported")
	}
}

// checkSuspendedIsPresent is the check for the most destructive failure this
// contract admits. A deactivated identity is still in the directory; a
// connector that omits it tells the host that the person left, and the host
// revokes everything they had.
//
// It requires the nominated principal to be PRESENT and to be reported
// INACTIVE. Present-and-active would mean the connector cannot see the
// deactivation at all, which leaves a host granting access to a suspended
// account — the opposite error, from the same missing field.
func checkSuspendedIsPresent(rep *Report, opts Options, r reads) {
	if r.principalsErr != nil {
		rep.add(CheckSuspendedIsPresent, false,
			"the principal list could not be read, so this check could not be driven — see "+CheckListsResolve)
		return
	}
	for _, p := range r.principals {
		if p.ID != opts.SuspendedPrincipal {
			continue
		}
		if p.Active {
			rep.add(CheckSuspendedIsPresent, false,
				"the principal this run nominates as DEACTIVATED is reported with Active true. A host that cannot "+
					"see a deactivation leaves a suspended account holding everything it had")
			return
		}
		rep.add(CheckSuspendedIsPresent, true, "the nominated deactivated principal is reported, with Active false")
		return
	}
	rep.add(CheckSuspendedIsPresent, false,
		"the principal this run nominates as deactivated is ABSENT from ListPrincipals. Suspended is not absent: a "+
			"deactivated identity is still in the directory, and omitting it tells the host the person left the "+
			"organisation — after which a host sweeping for departures revokes everything belonging to somebody who "+
			"is on leave. Report them with Active false")
}

func checkFailureTyped(ctx context.Context, rep *Report, c roster.Roster, opts Options) {
	res, err := c.ListMemberships(ctx, opts.AbsentGroup)
	readable := resolutionReadable(res)
	switch {
	case err == nil && !readable:
		rep.add(CheckFailureTyped, false, fmt.Sprintf(
			"listing the memberships of %q returned neither a list nor an error. That is the shape an unauthenticated "+
				"or misdirected read produces, and it must be a typed failure", opts.AbsentGroup))
	case err == nil:
		rep.add(CheckFailureTyped, false, fmt.Sprintf(
			"%q is declared absent by this run, and the connector answered for it. An empty roster is not the answer "+
				"to a group that does not exist: a reconcile loop given one removes the access that group carried",
			opts.AbsentGroup))
	case !cerr.Classified(err):
		rep.add(CheckFailureTyped, false, fmt.Sprintf(
			"failure is not classified: %v. A host must act on a cerr.Kind from the closed vocabulary; returning the "+
				"backend's own text leaves it string-matching, and a vendor rewording silently changes host behaviour", err))
	case cerr.KindOf(err) == cerr.KindContractViolation:
		rep.add(CheckFailureTyped, false, fmt.Sprintf(
			"failure is classified as %s, which is what a host reports ABOUT a connector, not a failure a connector returns: %v",
			cerr.KindContractViolation, err))
	default:
		rep.add(CheckFailureTyped, true, fmt.Sprintf("%q -> %s", opts.AbsentGroup, cerr.KindOf(err)))
	}
}

// listFailureDetail renders a list-operation failure, separating a contract
// violation from an honest failure so an author knows which of the two they
// are looking at.
func listFailureDetail(op string, err error) string {
	var fe *roster.FaultError
	if errors.As(err, &fe) {
		return fmt.Sprintf(
			"%s: %v. An unresolved roster must be reported as a failure — a read that came back empty because it was "+
				"unauthenticated, throttled or misdirected is not a directory with nobody in it, and a host that treats "+
				"it as one revokes access for everyone", op, fe)
	}
	return fmt.Sprintf("%s failed, and this run cannot conclude anything about a connector that reads nothing: %v", op, err)
}

func declaredIn(inManifest, atRuntime bool) string {
	switch {
	case inManifest && atRuntime:
		return "in the manifest and by Capabilities()"
	case inManifest:
		return "in the manifest but not by Capabilities()"
	case atRuntime:
		return "by Capabilities() but not in the manifest"
	default:
		return "nowhere"
	}
}

// resolutionReadable reports whether res carries a list.
func resolutionReadable(res roster.Resolution[roster.Membership]) bool {
	_, err := res.Items()
	return err == nil
}
