package codeci

import (
	"context"
	"fmt"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// A CheckPublication is one check state a publisher asserts against an exact
// commit.
//
// ⚠️ It is deliberately addressed by HeadSHA and not by branch or change-request
// number. A check published against a branch is a claim about whatever that
// branch pointed at when the host processed the call, and a branch moves — so a
// caller that decided about one tree can have its verdict attach to another. The
// commit is the only address that cannot move underneath the decision.
type CheckPublication struct {
	// Name is the check's context name — the string a ruleset requires, and
	// therefore the string that must match exactly. A publication under a name
	// no ruleset requires is a decoration nobody waits for; a ruleset naming a
	// check nobody publishes blocks forever.
	Name string
	// HeadSHA is the exact commit this check state is about.
	HeadSHA string
	// Status is the state being asserted. It uses the class's existing closed
	// [RunStatus] vocabulary rather than a separate publish-side one: a
	// publisher and a reader disagreeing about what "success" is called would
	// be a defect with no compiler to catch it.
	Status RunStatus
	// Title is the short headline the host shows beside the check.
	Title string
	// Summary is the longer body. It MAY be empty.
	Summary string
	// DetailsURL points at the full report. It MAY be empty.
	DetailsURL string
	// ExternalID is the publisher's own identifier for this check state.
	//
	// It is what makes republishing safe: a publisher that retries after an
	// ambiguous response needs the host to recognise the second call as the
	// same assertion rather than as a second check. A publisher that cannot
	// tell whether its first call landed, and has no way to say "this is that
	// one again", must either risk a duplicate or risk no check at all.
	ExternalID string
}

// requiredCheckPublicationFields is the single source of truth for what a
// publication must carry: [CheckPublication.Validate] derives from it, so a
// field cannot be half-required. The names are wire names, not Go field names,
// so an operator reading a refusal sees what they configured.
var requiredCheckPublicationFields = []struct {
	name string
	get  func(CheckPublication) string
}{
	{"name", func(p CheckPublication) string { return p.Name }},
	{"head_sha", func(p CheckPublication) string { return p.HeadSHA }},
}

// Validate reports whether p carries everything needed to publish a check, as a
// cerr.KindInvalid failure naming EVERY problem rather than the first.
//
// ⚠️ Status is validated too, and [RunStatusUnspecified] is REFUSED. It is the
// zero value, so a publisher that forgot to set it would otherwise publish
// "unclassified" against a commit — and a required check sitting in a state no
// ruleset recognises blocks the change request forever, with nothing in the log
// to say why. Refusing at the boundary turns a permanent stall into an
// immediate, named failure.
//
// Title, Summary, DetailsURL and ExternalID are not checked: each has a real
// zero value.
func (p CheckPublication) Validate() error {
	var problems []string
	for _, f := range requiredCheckPublicationFields {
		if f.get(p) == "" {
			problems = append(problems, "missing "+f.name)
		}
	}
	switch {
	case !p.Status.Valid():
		problems = append(problems, fmt.Sprintf("status %s is outside the closed vocabulary", p.Status))
	case p.Status == RunStatusUnspecified:
		problems = append(problems, "status is unspecified, which would publish an unclassified check "+
			"that a required-check rule can never be satisfied by")
	}
	if len(problems) == 0 {
		return nil
	}
	return cerr.New(cerr.KindInvalid, "PublishCheck", fmt.Errorf(
		"check publication is not publishable: %s", strings.Join(problems, "; ")))
}

// CheckPublisher is the optional contract operation behind [CapCheckPublish]:
// WRITE a check state against a commit, rather than merely read one.
//
// A connector that can do this does two things, and must do both: implement
// this interface, and declare [CapCheckPublish] in its manifest and from
// Capabilities(). Declaring without implementing is worse than declaring
// nothing — the host plans to publish the verdict it just computed and finds no
// operation to publish it with, after the decision is already made.
//
// # Why this is a separate tier from [CIController]
//
// Both mutate CI-adjacent state, and they are still different permissions.
// [CIController] re-runs or cancels a run the host DID NOT create; this
// publishes a check the connector's own identity owns. On the code host this
// contract is written against, the second is available to an app installation
// that cannot do the first, and the first is available to a token that cannot do
// the second — so a connector that could do either would have to declare both to
// say which, and a host reading one capability would learn nothing about the
// other.
//
// ⚠️ It is also a NEW interface rather than a method on [CIController], and that
// is not a stylistic choice: adding a method to an existing optional tier breaks
// every connector already implementing it, silently from this module's point of
// view — the failure appears at compile time in someone else's repository, after
// release.
type CheckPublisher interface {
	// PublishCheck asserts pub against the named repository, creating the check
	// or updating the one it already published, and returns the resulting
	// [CheckRun].
	//
	// ⚠️ It MUST validate pub before doing anything else, refusing an invalid
	// publication with cerr.KindInvalid and publishing nothing. Same shape as
	// [CodeCI.MergePR]'s and [CodeCI.CreatePR]'s guards, for the same reason: a
	// check is attached to a commit that other people are waiting on.
	//
	// ⚠️ It MUST BE IDEMPOTENT on [CheckPublication.ExternalID]. Republishing
	// the same external id against the same commit updates that check; it does
	// not create a second one. A publisher cannot know whether an ambiguous
	// first call landed, so it will retry — and a contract that produced a
	// duplicate check on retry would make every retry visibly wrong on the
	// change request, which is exactly when someone is watching.
	//
	// A repository or commit that does not exist is cerr.KindNotFound; a
	// credential that may not publish is cerr.KindUnauthorized.
	PublishCheck(ctx context.Context, fullName string, pub CheckPublication) (CheckRun, error)
}
