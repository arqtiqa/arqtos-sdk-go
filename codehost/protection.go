package codehost

import (
	"context"
	"fmt"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// A BypassActor is one principal a ruleset permits to bypass the rules it
// otherwise enforces on a governed ref.
//
// ⚠️ The interesting state of this type is the EMPTY LIST, and reading an empty
// list is the entire point of [ProtectionInspector] — see [Protection] for why
// that makes the fail-closed wrapper load-bearing rather than ceremonial.
type BypassActor struct {
	// ID is the host's identifier for the actor. It is a string because hosts
	// disagree about the shape: a numeric team or app id on one, a role name
	// on another.
	ID string
	// Kind says what sort of principal ID names — a team, an app installation,
	// a role, an individual. It is the host's own word, passed through: a
	// normalised vocabulary here would have to guess, and a bypass actor a
	// reader cannot categorise is one they will investigate rather than
	// dismiss, which is the safer failure.
	Kind string
	// Description is the host's human-readable label, where it gives one.
	Description string
}

// A RequiredCheck is one status check a ruleset requires before a governed ref
// may move.
type RequiredCheck struct {
	// Context is the check's name, as the ruleset names it and as the
	// publishing side must publish it.
	Context string
	// AppID is the identity of the app the check is PINNED to, empty when the
	// ruleset pins the check to no app at all.
	//
	// ⚠️ This field is the difference between a check that gates and a check
	// that decorates. An unpinned status context is matched by NAME, so any
	// credential that can write a status to the repository can satisfy it —
	// which is why an unpinned required check is not enforcement, and why the
	// probe reads this rather than merely confirming the check is required.
	AppID string
}

// Pinned reports whether the check is bound to a specific app.
//
// An unpinned required check can be satisfied by any repository token, so it
// constrains nobody who already holds one.
func (c RequiredCheck) Pinned() bool { return c.AppID != "" }

// PinnedTo reports whether the check is bound to appID specifically.
//
// ⚠️ Pinned to SOME app is not the assurance claim; pinned to the EXPECTED app
// is. A check pinned to a different app is enforcement on someone else's behalf.
func (c RequiredCheck) PinnedTo(appID string) bool {
	return appID != "" && c.AppID == appID
}

// A Protection is a governed ref's enforcement configuration, as the host holds
// it — the two dimensions an assurance probe has to read together.
//
// ⚠️ BOTH LISTS ARE FAIL-CLOSED, and that is the reason this type exists rather
// than a pair of plain slices. The claim the probe makes is "this ref requires a
// check pinned to our app, AND nothing may bypass it". The second half is an
// assertion about an EMPTY list — and a plain `[]BypassActor` returned by a read
// that failed is also empty. The two are indistinguishable at the call site, and
// they are opposite: one says nobody can bypass the gate, the other says nobody
// looked.
//
// So a connector cannot report an empty bypass list by accident. It reports one
// by calling [EmptyList], which is an assertion, and a caller cannot read a
// resolution that was never resolved.
type Protection struct {
	// Ref is the governed ref this protection applies to, echoed back so a
	// caller holding several cannot mix them up.
	Ref string
	// RequiredChecks is the set of checks that must pass before Ref moves.
	RequiredChecks Resolution[RequiredCheck]
	// BypassActors is the set of principals permitted to bypass those rules.
	// An empty, RESOLVED list is the strong claim; an unresolved one is not a
	// weaker version of it, it is the absence of a claim.
	BypassActors Resolution[BypassActor]
}

// CheckProtection is the host-side guard on a [Protection], and it exists for
// the reason the CodeCI class's CheckIdentity guard exists: a Protection is a plain struct whose
// fields read fine.
//
// ⚠️ It refuses a Protection whose lists were never resolved. Without it, a
// caller writing the obvious code —
//
//	p, err := insp.InspectProtection(ctx, repo, ref)
//	if err == nil && len(p.BypassActors.Items()) == 0 { /* assurance: high */ }
//
// concludes that nothing can bypass the gate from a read that returned nothing.
// This turns that into a typed failure at the boundary.
// ⚠️ Its refusals are cerr.KindInvalid, not bare errors, and that matters more
// here than it looks. The caller this guard exists for is the one writing
//
//	if err == nil && len(p.BypassActors.Items()) == 0 { /* assurance: high */ }
//
// and that caller classifies failures with cerr.KindOf like every other call in
// this contract. A bare error would classify as cerr.KindUnknown — which does
// not trip a breaker and reads as "something odd happened" rather than "this
// value is not usable" — so the refusal would be weaker precisely for the
// audience it was written for. The Items() failure stays wrapped underneath, so
// errors.Is still reaches it.
func CheckProtection(p Protection) error {
	if p.Ref == "" {
		return cerr.New(cerr.KindInvalid, "CheckProtection", fmt.Errorf(
			"protection names no ref: a caller holding several cannot tell them apart"))
	}
	if _, err := p.RequiredChecks.Items(); err != nil {
		return cerr.New(cerr.KindInvalid, "CheckProtection", fmt.Errorf(
			"required checks were not resolved, so this protection cannot support a claim "+
				"about what gates %s: %w", p.Ref, err))
	}
	if _, err := p.BypassActors.Items(); err != nil {
		return cerr.New(cerr.KindInvalid, "CheckProtection", fmt.Errorf(
			"bypass actors were not resolved for %s — an unread bypass list must never be "+
				"read as an empty one, because the two are opposite claims: %w", p.Ref, err))
	}
	return nil
}

// ProtectionInspector is the optional contract operation behind
// [CapProtectionInspect]: READ a governed ref's enforcement configuration.
//
// A connector that can do this does two things, and must do both: implement this
// interface, and declare [CapProtectionInspect] in its manifest and from
// Capabilities(). Declaring without implementing is worse than declaring
// nothing — a host that plans to probe its own assurance finds no operation to
// do it with, at the moment it is trying to establish whether it is protected.
//
// ⚠️ This is a READ-ONLY tier, deliberately. Writing protection configuration is
// a different and far larger authority: a connector that could relax a ruleset
// could disable the gate it is being asked to report on. That operation is not
// in this contract and should not be added to it.
type ProtectionInspector interface {
	// InspectProtection reports the enforcement configuration the host holds
	// for ref in the named repository.
	//
	// ⚠️ It MUST NOT report an empty bypass-actor list for a read it did not
	// complete. A host that cannot enumerate bypass actors returns a typed
	// failure; it does not return a Protection whose BypassActors is an
	// unasserted empty list. Genuine emptiness — the strong and common case —
	// is reported with [EmptyList].
	//
	// A ref carrying no protection at all is NOT an error: it is a Protection
	// with resolved, empty required checks, which is a real and important
	// answer. A ref or repository that does not exist is cerr.KindNotFound;
	// one the credential cannot read is cerr.KindUnauthorized.
	//
	// ⚠️ cerr.KindUnauthorized matters more here than elsewhere. A credential
	// that cannot see a ruleset reads exactly like a ref with no ruleset, and
	// the second is the answer that downgrades an assurance claim to nothing.
	// A connector MUST distinguish them.
	InspectProtection(ctx context.Context, fullName, ref string) (Protection, error)
}
