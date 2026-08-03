package tracker

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// This file holds the two properties of [TrainAdmin] that are part of the
// CONTRACT rather than each backend's business, as host-side guards a caller
// runs over whatever a connector returned.
//
// Both are stated in [TrainAdmin]'s own documentation, and a property that
// lives only in documentation is one every backend re-derives. These are the
// enforcing half:
//
//   - UNION WITH UNKNOWN DOMINATING — [CheckTrainSets]. The train set is a
//     union over scopes, so the less of it a caller can see the greener a
//     replan looks. A scope that could not be read must come back with
//     [ScopeTrains.Err] set, and it must come back: silently dropping the
//     entry computes the same answer as reporting it empty, which is "nothing
//     to create here".
//   - VERIFY BY RE-READING — [CheckTrainsCreated]. A create loop that iterated
//     once still returns successfully for every name it was given, and the
//     count is the only thing that shows it — so the count is checked against
//     the demand, and then the names are checked against the tracker.

// CheckTrainSets is the host-side guard for [TrainAdmin.ListTrains]: it rejects
// an answer that does not account for the scopes it was asked about, and names
// the connector that produced it.
//
// # What it enforces, and why none of it is the backend's business
//
// The train set is a UNION over scopes. A caller planning a train move reads it
// to answer "which scopes are missing this bucket", so every scope the answer
// leaves out is a scope the plan silently treats as needing nothing. That makes
// three shapes indistinguishable at the call site and all three wrong:
//
//   - a requested scope with NO ENTRY at all;
//   - an entry whose [ScopeTrains.Err] is set AND whose Trains are non-nil —
//     unknown reported as partly known, which a caller reads as known;
//   - two entries for one scope, one saying Err and one saying empty. A union
//     built from those depends on iteration order.
//
// It also holds the answer's SHAPE to what the connector declared.
// [CapScopedTrains] says trains are per-scope, so the answer must carry one
// entry per requested scope; a connector without it reports trains under the
// zero [Scope] because they are not partitioned, so the answer must be exactly
// one entry under that zero Scope. A host that read a per-scope answer from an
// unpartitioned connector — or the reverse — would plan a create per scope
// against a backend with one bucket namespace, or one create against a backend
// that needs eight.
//
// caps is the connector's own [connector.Connector.Capabilities], not a
// boolean, so a host cannot get the polarity backwards.
//
// requested is the scopes the CALLER asked about, and it is separate for the
// same reason [CheckApplyReport] takes the demand separately: an answer checked
// only against itself is self-consistent by construction, and a connector that
// returned one entry would render clean for a request naming eight scopes.
//
// On the conformant path it returns the resolution UNCHANGED — the same
// entries, in the same order — because that IS the answer a caller acts on.
func CheckTrainSets(
	connectorName, op string,
	caps connector.Capabilities,
	requested []Scope,
	r Resolution[ScopeTrains],
	err error,
) (Resolution[ScopeTrains], error) {
	checked, rerr := CheckResolution(connectorName, op, r, err)
	if rerr != nil {
		return Resolution[ScopeTrains]{}, rerr
	}
	sets, ierr := checked.Items()
	if ierr != nil {
		return Resolution[ScopeTrains]{}, attribute(connectorName, op, ierr)
	}

	fault := func(f Fault, detail string) (Resolution[ScopeTrains], error) {
		return Resolution[ScopeTrains]{}, &FaultError{
			Connector: connectorName,
			Op:        op,
			Fault:     f,
			Detail:    detail,
		}
	}

	// Unknown dominating, per entry. This runs before the accounting so that a
	// connector reporting one scope as both unknown and known is told about
	// that rather than about a count.
	for _, s := range sets {
		if s.Err == nil {
			continue
		}
		if s.Trains != nil {
			return fault(FaultUnknownAsKnown, fmt.Sprintf(
				"reported scope %s with an error AND %d train(s); unknown DOMINATES, so Trains is nil and "+
					"MEANINGLESS whenever Err is set — a caller that read the list beside the error would plan "+
					"against a set it was just told it could not see", quoteScope(s.Scope), len(s.Trains)))
		}
		if !cerr.Classified(s.Err) {
			return fault(FaultUnknownAsKnown, fmt.Sprintf(
				"reported scope %s as unreadable with an unclassified error (%v); a host routes on the "+
					"classification, and a failure it cannot classify is cerr.KindUnknown rather than a bare error",
				quoteScope(s.Scope), s.Err))
		}
	}

	scoped := caps.Has(CapScopedTrains)

	// The unpartitioned shape: one entry, under the zero Scope. It is checked
	// first because for such a connector the requested scopes are not the
	// partition of the answer at all, so accounting for them one by one would
	// demand a shape the contract forbids.
	if !scoped {
		switch {
		case len(sets) != 1:
			return fault(FaultScopeUnaccounted, fmt.Sprintf(
				"returned %d entr(ies) without declaring %s; trains are not partitioned on this connector, so the "+
					"whole answer is ONE entry under the zero scope — a per-scope answer here tells a host to plan "+
					"a create per scope against one bucket namespace", len(sets), CapScopedTrains))
		case sets[0].Scope != "":
			return fault(FaultScopeUnaccounted, fmt.Sprintf(
				"reported its single entry under scope %s without declaring %s; trains are reported under the ZERO "+
					"scope when they are not partitioned, and a named one makes an unpartitioned answer look like "+
					"the one scope a caller happened to ask about", quoteScope(sets[0].Scope), CapScopedTrains))
		}
		return checked, nil
	}

	// The partitioned shape: exactly one entry per requested scope, and nothing
	// else. Sorted, because an answer with two bad scopes must produce the same
	// message twice — a failure that reads differently on each run is a failure
	// two operators compare and disagree about.
	seen := make(map[Scope]int, len(sets))
	for _, s := range sets {
		seen[s.Scope]++
	}
	want := make(map[Scope]bool, len(requested))
	for _, s := range requested {
		want[s] = true
	}

	var missing []string
	for s := range want {
		if seen[s] == 0 {
			missing = append(missing, quoteScope(s))
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fault(FaultScopeUnaccounted, fmt.Sprintf(
			"was asked about %d scope(s) and reported nothing at all for %s; the set is a UNION over scopes, so a "+
				"scope with no entry is read as a scope needing nothing — exactly the answer a scope that could not "+
				"be read must NOT produce (say so with ScopeTrains.Err)", len(requested), strings.Join(missing, ", ")))
	}

	var dupes, foreign []string
	for s, n := range seen {
		if n > 1 {
			dupes = append(dupes, fmt.Sprintf("%s (%d times)", quoteScope(s), n))
		}
		if !want[s] {
			foreign = append(foreign, quoteScope(s))
		}
	}
	if len(dupes) > 0 {
		slices.Sort(dupes)
		return fault(FaultScopeUnaccounted, fmt.Sprintf(
			"reported scope %s more than once; two entries for one scope can disagree — one unreadable and one "+
				"empty — and which of them a union sees depends on iteration order", strings.Join(dupes, ", ")))
	}
	if len(foreign) > 0 {
		slices.Sort(foreign)
		return fault(FaultScopeUnaccounted, fmt.Sprintf(
			"reported scope %s, which was not asked about; an answer about something else is not an answer, and a "+
				"caller cannot tell it from the scope it did ask about", strings.Join(foreign, ", ")))
	}
	return checked, nil
}

// CheckTrainsCreated is the host-side guard for [TrainAdmin.CreateTrains], and
// it is the only guard in this contract that makes a call of its own.
//
// # Why it re-reads
//
// [TrainAdmin.CreateTrains] says a create must be verified by RE-READING, and
// the reason is stated in the failure it prevents: a create loop that iterated
// once still returns successfully for every name it was given, and the count is
// the only thing that shows it. So this guard does both halves, in order:
//
//  1. the COUNT, through [CheckApplyReport] against the real demand — the same
//     arithmetic every write path in this contract is held to, which catches a
//     report that accounts for fewer specs than were asked for;
//  2. the NAMES, by calling [TrainAdmin.ListTrains] and looking for every spec
//     the report counted as applied.
//
// The count alone cannot catch the loop. A connector that created the first
// bucket and then reported Requested=8, Applied=8, Failed={} closes
// arithmetically and is wrong about seven scopes. Only the re-read sees it, and
// leaving the re-read to each backend is what makes it a property nobody has.
//
// # What it does when the re-read itself fails
//
// It returns a ZEROED report and an error WRAPPING the read failure, so
// cerr.KindOf still answers the read's own classification. That is deliberately
// neither outcome: the create is not reported clean, because nothing confirmed
// it, and it is not reported failed, because it may well have worked. Unknown
// is never clean — a caller that took an unverifiable create as done would plan
// the next move against buckets that may not exist.
//
// A conformant connector whose specs were all refused pre-network reports
// Applied=0, and this guard then makes NO call at all: there is nothing to
// verify, and a read issued anyway would be a round trip per rejected batch.
func CheckTrainsCreated(
	ctx context.Context,
	connectorName string,
	admin TrainAdmin,
	caps connector.Capabilities,
	specs []TrainSpec,
	rep ApplyReport,
	err error,
) (ApplyReport, error) {
	const op = "CreateTrains"

	checked, aerr := CheckApplyReport(connectorName, op, len(specs), rep, err)
	if aerr != nil {
		return ApplyReport{}, aerr
	}
	if checked.Applied == 0 {
		return checked, nil
	}
	if admin == nil {
		return ApplyReport{}, cerr.New(cerr.KindInvalid, op, fmt.Errorf(
			"connector %q reported %d train(s) created and no TrainAdmin was supplied to re-read them; a create this "+
				"guard cannot verify is not a create it may report as done", nameOr(connectorName), checked.Applied))
	}

	// Only the scopes of specs the report did NOT attribute a failure to. A
	// spec that failed is not expected on the tracker, and asking about its
	// scope would demand a read the connector was never required to serve.
	applied := make([]TrainSpec, 0, len(specs))
	for i, spec := range specs {
		if _, failed := checked.Failed[i]; !failed {
			applied = append(applied, spec)
		}
	}
	scopes := make([]Scope, 0, len(applied))
	for _, spec := range applied {
		if !slices.Contains(scopes, spec.Scope) {
			scopes = append(scopes, spec.Scope)
		}
	}

	res, lerr := admin.ListTrains(ctx, scopes)
	sets, lerr := CheckTrainSets(connectorName, "ListTrains", caps, scopes, res, lerr)
	if lerr != nil {
		return ApplyReport{}, fmt.Errorf(
			"connector %q reported %d train(s) created and the re-read that would verify them failed, so the outcome "+
				"is UNKNOWN rather than done: %w", nameOr(connectorName), checked.Applied, lerr)
	}
	entries, ierr := sets.Items()
	if ierr != nil {
		return ApplyReport{}, attribute(connectorName, "ListTrains", ierr)
	}

	// A conformant ListTrains has already been checked to carry every scope
	// asked about, so a name that is absent is absent from the tracker.
	have := map[Scope][]string{}
	for _, e := range entries {
		for _, tr := range e.Trains {
			have[e.Scope] = append(have[e.Scope], tr.Name)
		}
	}
	scoped := caps.Has(CapScopedTrains)
	var absent []string
	for _, spec := range applied {
		key := spec.Scope
		if !scoped {
			key = ""
		}
		if !slices.Contains(have[key], spec.Name) {
			absent = append(absent, quoteTrain(spec, scoped))
		}
	}
	if len(absent) > 0 {
		slices.Sort(absent)
		return ApplyReport{}, &FaultError{
			Connector: connectorName,
			Op:        op,
			Fault:     FaultCreateUnverified,
			Detail: fmt.Sprintf(
				"reported %d of %d train(s) created and a re-read does not find %s; a create loop that iterated "+
					"once returns successfully for every name it was given, so the report is not evidence and the "+
					"re-read is", checked.Applied, checked.Requested, strings.Join(absent, ", ")),
		}
	}
	return checked, nil
}

func quoteScope(s Scope) string {
	if s == "" {
		return "the zero scope"
	}
	return strconv.Quote(string(s))
}

func quoteTrain(spec TrainSpec, scoped bool) string {
	if scoped {
		return strconv.Quote(spec.Name) + " in scope " + quoteScope(spec.Scope)
	}
	return strconv.Quote(spec.Name)
}

func nameOr(connectorName string) string {
	if connectorName == "" {
		return "<unnamed>"
	}
	return connectorName
}
