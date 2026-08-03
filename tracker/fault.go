package tracker

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// A Fault names a way a connector can break the Tracker contract: not a
// failure it reports (those are cerr Kinds, chosen by the connector), but a
// return the contract does not admit at all.
//
// The set is closed for the same reason the failure vocabulary is: a host acts
// on the name, and adding one is a change to a published contract.
//
// The two list faults are spelled with the SAME strings the roster
// package uses, deliberately. [Resolution] IS that package's type, so the
// faults [Resolved] and Resolution.Items raise are its faults, and
// [CheckResolution] translates them onto this vocabulary by name — see
// TestFaultNames_MatchTheSDKsSoTranslationIsTotal, which is what keeps that
// translation total if roster ever renames one.
//
// The roster package also publishes faults of its OWN class that mean nothing
// here, so the translation is bounded by [Fault.Valid]: a name outside this set
// is refused rather than converted, because a closed vocabulary a host cannot
// exhaust is not closed.
type Fault string

const (
	// FaultUnresolved is a connector reporting success while carrying no
	// list — the (empty list, no error) shape this contract exists to
	// eliminate. An audit or a rollup over that reading reports every item it
	// never read as compliant.
	FaultUnresolved Fault = "unresolved-without-error"
	// FaultPartial is a connector asserting [Partial] on a call to
	// [Resolved] — a board read that stopped before covering everything it is
	// meant to, reported as a success rather than as the typed failure the
	// contract requires for one.
	FaultPartial Fault = "partial-read-reported-as-success"
	// FaultUnattributed is an [ApplyReport] whose arithmetic does not close: a
	// report counting a different demand than was actually made, a change it
	// was asked for that it lists neither as applied nor as failed, a failure
	// attributed to an index naming no change, or a failure listed with no
	// reason.
	//
	// It is this class's own fault, and it is here because [Tracker.Apply] is
	// the one operation in the contract that is not transactional. "A change
	// not listed as failed was applied" is the rule a caller acts on, so a
	// report that accounts for fewer changes than it was asked for does not
	// merely lose information — it converts a failure into a success.
	FaultUnattributed Fault = "change-outcome-unattributed"

	// FaultScopeUnaccounted is a [TrainAdmin.ListTrains] answer that does not
	// account for the scopes it was asked about: a requested scope with no
	// entry at all, two entries for one scope, an entry naming a scope nobody
	// asked about, or a partition shape that contradicts what the connector
	// declared with [CapScopedTrains].
	//
	// It is a fault rather than a smaller answer because the train set is a
	// UNION over scopes. A caller reads it to answer "which scopes are missing
	// this bucket", so a scope with no entry is read as a scope needing
	// nothing: the same answer a scope that could not be read must never
	// produce. The less of the set a caller can see, the greener a replan
	// looks. See [CheckTrainSets].
	FaultScopeUnaccounted Fault = "scope-outcome-unaccounted"

	// FaultUnknownAsKnown is a [ScopeTrains] entry that reports a scope as
	// unreadable and hands out something readable anyway: [ScopeTrains.Err]
	// set beside a non-nil Trains, or an Err a host cannot classify.
	//
	// Unknown DOMINATES in this contract. An entry carrying both is not extra
	// information — it is a caller planning against a set it was just told it
	// could not see. See [CheckTrainSets].
	FaultUnknownAsKnown Fault = "unknown-reported-as-known"

	// FaultCreateUnverified is a [TrainAdmin.CreateTrains] report counting a
	// bucket as created that a re-read does not find.
	//
	// It is this contract's own fault and it is the one that needs a second
	// call to see. A create loop that iterated once still returns successfully
	// for every name it was given and closes arithmetically, so the count is
	// the only thing that shows it and the count alone is not enough. See
	// [CheckTrainsCreated].
	FaultCreateUnverified Fault = "create-reported-without-re-reading"
)

// faults is this contract's closed Fault vocabulary and the only thing
// [Fault.Valid] answers from, so a fault cannot be half-added.
//
// It is load-bearing rather than documentation: the translation in attribute
// maps roster's faults onto this vocabulary BY NAME, and the roster
// package publishes faults this contract has no name for
// (membership-is-for-another-group and the two undeclared-capability ones).
// Converting one of those by name would mint a Fault outside the closed set —
// a name a host switches on and does not recognise — so attribute checks
// membership here and REFUSES what is not in it.
var faults = map[Fault]bool{
	FaultUnresolved:       true,
	FaultPartial:          true,
	FaultUnattributed:     true,
	FaultScopeUnaccounted: true,
	FaultUnknownAsKnown:   true,
	FaultCreateUnverified: true,
}

// Valid reports whether f names a fault in this contract's closed vocabulary.
func (f Fault) Valid() bool { return faults[f] }

// faultDetails is this contract's own wording for each fault, and it is why
// the translation in attribute does not simply carry roster's detail across.
//
// The roster package raises the two list faults from its own class, whose details are
// written about directories and principals: "must say so with EmptyRoster", "a
// host cannot safely revoke access for someone it never got to examine". Both
// sentences are the right advice in the wrong domain, and an operator reading
// one on a board scan goes looking for an identity provider that is not there.
// The blast radius is stated here in the terms this class actually harms: an
// audit that passes over items it never read, and a rollup that completes a
// parent over a child set it could not see.
var faultDetails = map[Fault]string{
	FaultUnresolved: "returned no list and no error; an unresolved result is a failure, never a board that holds " +
		"nothing, and an audit or a rollup over the second reading reports every item it never read as compliant. " +
		"A tracker that genuinely holds none of the requested thing says so with EmptyList",
	FaultPartial: "reported a read that stopped before covering everything it is meant to as a success; a truncated " +
		"scan must surface as a typed failure (see the cerr package), because an audit over the shorter list reports " +
		"every item it never examined as compliant and a rollup over a truncated child set reports a parent complete",
}

// A FaultError reports a CONNECTOR CONTRACT VIOLATION, attributed by name.
//
// It is deliberately a distinct type rather than a generic error: coercing a
// broken connector's return into an ordinary failure would hide the breakage
// behind behaviour that looks correct, and the operator would go looking for
// the fault in the tracker, where it is not.
//
// Its Kind is cerr.KindContractViolation, so a host that only classifies —
// cerr.KindOf, cerr.Retryable, cerr.TripsBreaker — still gets the right
// answers: not retryable (a broken connector does not improve), and not
// breaker-tripping (this is not backend load).
type FaultError struct {
	// Connector is the host's name for the connector at fault. It is empty
	// when the fault was raised inside the connector itself, which does not
	// know what it was registered as; [CheckResolution] and
	// [CheckApplyReport] fill it in host-side.
	Connector string
	// Op is the contract operation, e.g. "Scan".
	Op string
	// Fault is which contract violation occurred.
	Fault Fault
	// Detail is optional context. It never carries item content.
	Detail string
}

func (e *FaultError) Error() string {
	who := "an unnamed connector"
	if e.Connector != "" {
		who = "connector " + strconv.Quote(e.Connector)
	}
	op := e.Op
	if op == "" {
		op = "a contract operation"
	}
	msg := fmt.Sprintf("%s violated the Tracker contract in %s: %s", who, op, e.Fault)
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}

// Unwrap exposes the fault to the cerr taxonomy, so classification-only host
// code sees cerr.KindContractViolation without knowing this type exists.
func (e *FaultError) Unwrap() error { return cerr.New(cerr.KindContractViolation, e.Op, nil) }

// CheckResolution is the host-side guard for a list return: it rejects a
// return the contract does not admit, and names the connector that made it.
//
// Pass it whatever a connector returned from a list operation, together with
// the name the host knows that connector by. It returns:
//
//   - the connector's own typed failure, unchanged, when err is non-nil and is
//     not a fault — a connector that failed correctly is not at fault;
//   - a [FaultError] naming connectorName, when the connector reported success
//     with nothing resolved, or raised a fault of its own (which carries no
//     name until here);
//   - the resolution UNCHANGED, when it is conformant — the same list, in the
//     same order, including a deliberately-empty one. This half is the whole
//     product of the call on the path that is not broken, and
//     TestCheckResolution_ReturnsTheListItWasGiven is what holds it: a version
//     that returned EmptyList[T]() here would be a silent, total board wipe
//     behind a guard whose stated job is to prevent one.
//
// A host that skips this guard is not exposed to a success carrying nothing:
// Resolution.Items refuses to read an unresolved resolution regardless. What
// the guard adds is attribution — which connector, in which operation — and
// ONE error type to match on: because [Resolution] is roster's, a fault
// raised inside it arrives as *roster.FaultError, and this is the single place
// that translates it rather than leaving every host to match two types for one
// contract.
func CheckResolution[T any](connectorName, op string, r Resolution[T], err error) (Resolution[T], error) {
	if err != nil {
		return Resolution[T]{}, attribute(connectorName, op, err)
	}
	// The roster guard is the presence oracle. Resolution is its type, so the
	// presence flag is unreachable from here, and reading Items() to find out
	// would copy every item on the board to answer a yes/no question. The
	// fault it would raise is not reused: its wording is about directories.
	if _, absent := roster.CheckResolution(connectorName, op, r, nil); absent != nil {
		return Resolution[T]{}, &FaultError{
			Connector: connectorName,
			Op:        op,
			Fault:     FaultUnresolved,
			Detail:    faultDetails[FaultUnresolved],
		}
	}
	return r, nil
}

// CheckApplyReport is the host-side guard for the WRITE path: it rejects an
// [ApplyReport] whose arithmetic does not close, and names the connector that
// produced it.
//
// It is the counterpart of [CheckResolution] and it exists for the same reason
// in the opposite direction. On a read, the dangerous default is that nothing
// means an empty list; on a write it is the reverse — [Tracker.Apply] is not
// transactional, so a change the report does not list as failed was APPLIED.
// A report that counts fewer outcomes than it was asked for therefore tells a
// caller that changes nobody can account for succeeded.
//
// requested is the DEMAND — len(changes), counted by the CALLER before
// anything was attempted — and it is a separate argument on purpose. Without
// it this guard could only check a report against itself, and a report checked
// against itself makes the zero report clean: ApplyReport{} with a nil error
// closes trivially, so a connector that swallowed all fifty changes would
// render CLEAN and the caller would read 0 requested / 0 applied / 0 failed as
// a clean answer for an outcome nobody determined. That is the estate's
// unknown-never-clean rule broken on the write path, while the read path is
// fail-closed by construction. A report disagreeing with requested is
// [FaultUnattributed].
//
// It returns the connector's own typed failure unchanged when err is non-nil,
// with a ZEROED report: an Apply that failed has no counts a caller may act
// on. On the conformant path it returns the report UNCHANGED — both counts and
// the whole Failed map — because that report IS the answer a caller acts on;
// see TestCheckApplyReport_AcceptsAReportWhoseArithmeticCloses.
func CheckApplyReport(connectorName, op string, requested int, r ApplyReport, err error) (ApplyReport, error) {
	if err != nil {
		return ApplyReport{}, attribute(connectorName, op, err)
	}
	fault := func(detail string) (ApplyReport, error) {
		return ApplyReport{}, &FaultError{
			Connector: connectorName,
			Op:        op,
			Fault:     FaultUnattributed,
			Detail:    detail,
		}
	}
	if requested < 0 || r.Requested < 0 || r.Applied < 0 {
		return fault(fmt.Sprintf("reported Requested=%d and Applied=%d against %d change(s) asked for; "+
			"none of those is a count of changes", r.Requested, r.Applied, requested))
	}
	if r.Requested != requested {
		return fault(fmt.Sprintf("reported Requested=%d, and %d change(s) were asked for; the demand is what the "+
			"report is checked against, because a report that supplies its own demand can account for nothing and "+
			"still close", r.Requested, requested))
	}
	// Sorted, because a report with two bad indices must produce the same
	// message twice — a failure that reads differently on each run is a
	// failure two operators compare and disagree about.
	indices := make([]int, 0, len(r.Failed))
	for i := range r.Failed {
		indices = append(indices, i)
	}
	slices.Sort(indices)
	for _, i := range indices {
		if i < 0 || i >= r.Requested {
			return fault(fmt.Sprintf("attributed a failure to change %d, and %d change(s) were requested; "+
				"an index that names no change attributes the failure to nothing", i, r.Requested))
		}
		if r.Failed[i] == nil {
			return fault(fmt.Sprintf("listed change %d as failed with no reason; a failure a host cannot "+
				"classify is cerr.KindUnknown, never a nil error", i))
		}
	}
	if r.Applied+len(r.Failed) != r.Requested {
		return fault(fmt.Sprintf("reported %d of %d change(s) applied and %d failed, leaving %d unaccounted for; "+
			"a change this report does not list as failed was applied, so an unaccounted change is reported as a success",
			r.Applied, r.Requested, len(r.Failed), r.Requested-r.Applied-len(r.Failed)))
	}
	return r, nil
}

// attribute names the connector on a fault that was raised without one, and
// leaves every other error alone. It returns a named copy rather than mutating
// the caller's error.
//
// It also translates roster's fault into this contract's, for the reason
// [CheckResolution] documents: [Resolved] and Resolution.Items are forwards,
// so the faults they raise are *roster.FaultError, and a host must not have to
// match two error types for one contract. The Fault names are the same strings
// on both sides for the two faults an aliased Resolution can raise, so the
// translation loses nothing there — and it goes no further than that: a roster
// fault outside this contract's closed vocabulary ([Fault.Valid]) is REFUSED
// rather than converted, because the alternative is a Fault a host does not
// recognise.
func attribute(connectorName, op string, err error) error {
	var fe *FaultError
	if errors.As(err, &fe) {
		if fe.Connector != "" {
			return err
		}
		named := *fe
		named.Connector = connectorName
		if named.Op == "" {
			named.Op = op
		}
		return &named
	}
	var rfe *roster.FaultError
	if errors.As(err, &rfe) {
		f := Fault(rfe.Fault)
		if !f.Valid() {
			// REFUSED, not translated, and this is the boundary of the
			// translation's totality. The mapping is BY NAME, and the roster
			// package publishes faults this contract has no name for, so
			// converting one would hand a host a tracker.Fault outside the
			// closed vocabulary — the single thing a closed vocabulary exists
			// to prevent, and worse than the two error types the translation
			// was written to avoid.
			//
			// The SDK's own error is wrapped rather than replaced, so nothing
			// is lost: cerr.KindOf still answers KindContractViolation, an
			// errors.As for *roster.FaultError still reaches the fault and its
			// detail, and no Fault of this contract is minted.
			who := strconv.Quote(connectorName)
			if connectorName == "" {
				who = "an unnamed connector"
			}
			return fmt.Errorf("%s raised %q in %s, which is not a fault of the Tracker contract, so it is refused "+
				"rather than translated onto a name this contract does not publish: %w", who, rfe.Fault, op, err)
		}
		// op, not rfe.Op. A fault raised inside roster's Resolution is
		// stamped with that package's own generic operation name ("List"),
		// which is not an operation of this contract — reporting it would
		// name an operation no host can look up. The op the guard was given
		// is the one that was actually called.
		translated := &FaultError{
			Connector: rfe.Connector,
			Op:        op,
			Fault:     f,
			Detail:    faultDetails[f],
		}
		if translated.Connector == "" {
			translated.Connector = connectorName
		}
		if translated.Detail == "" {
			// A fault this contract names but has no wording of its own for:
			// faultDetails covers the two an aliased Resolution raises, and
			// FaultUnattributed's detail is per-instance because it reports
			// arithmetic. Keep roster's words rather than none — an
			// unexplained fault is worse than one explained in the wrong
			// domain.
			translated.Detail = rfe.Detail
		}
		return translated
	}
	return err
}
