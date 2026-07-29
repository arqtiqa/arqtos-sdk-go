package rosterconform

import (
	"context"
	"fmt"
	"os/exec"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
	"github.com/arqtiqa/arqtos-sdk-go/roster"
)

// A Provider names an out-of-process (Track-B) Roster provider to launch and
// dial.
type Provider struct {
	// Path is the provider EXECUTABLE. Build it first — this launches a
	// binary, it does not compile one.
	Path string
	// Args are passed to the executable. Most providers need none: a
	// go-plugin provider is configured by its environment and its own
	// config, not by argv.
	Args []string
	// HostVersion is the contract version the host doing the dialling
	// implements, and it is REQUIRED: it is one half of the min_host_version
	// negotiation, and a run that skipped the negotiation would be proving
	// conformance of a pairing a real host would have refused.
	HostVersion string
}

// RunOutOfProcess launches the provider named by p, dials it over
// go-plugin/gRPC exactly as a host does, and runs every check in [Run] against
// the dispensed host stub.
//
// # Why this exists rather than leaving it to the caller
//
// Two reasons, and the second is the one that made it an SDK API rather than a
// snippet in a connector's own test.
//
// A conformance run that never serialised anything cannot see a marshalling
// bug, and marshalling is where this class's worst failures live. An
// unresolved read arriving as an empty directory, a suspended principal
// losing its Active flag, a membership arriving for a group nobody asked
// about: every one of them is invisible to an in-process run of the same
// connector, and every one of them ends in a host removing access it should
// have kept. A connector shipped as a provider therefore has to be checked as
// one.
//
// And the spawning has to be reachable from HERE. A connector repository
// forbids os/exec in its own package tree — a connector must not shell out,
// and the ban is enforced by a test that recurses into the command
// directories too — so a connector's own CI cannot spawn its own binary. The
// exec lives on the SDK side of that boundary, once, where it is the harness
// doing it rather than the connector.
//
// # What the returned error means
//
// It is non-nil when the run could not be CARRIED OUT: no path, no host
// version, a binary that will not launch, or a provider the min_host_version
// negotiation refused. That last one is not a conformance failure — a refused
// provider was never dialled, so there is nothing to report checks about, and
// a Report full of failures would misattribute a correct refusal to broken
// behaviour.
//
// A provider that runs and is non-conformant yields a nil error and a Report
// whose Err reports the failures, the same as [Run]. Gate on Report.Err.
func RunOutOfProcess(ctx context.Context, p Provider, opts Options) (Report, error) {
	if p.Path == "" {
		return Report{}, cerr.New(cerr.KindInvalid, "rosterconform.RunOutOfProcess",
			fmt.Errorf("Provider.Path is unset: this launches a provider executable, it does not build one"))
	}
	if p.HostVersion == "" {
		return Report{}, cerr.New(cerr.KindInvalid, "rosterconform.RunOutOfProcess", fmt.Errorf(
			"Provider.HostVersion is unset, so the provider's min_host_version cannot be negotiated; a run that "+
				"skips the negotiation proves conformance of a pairing a real host would have refused"))
	}

	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: plugin.Handshake,
		// The HOST-side map: it carries the manifest and this host's version,
		// so Dispense performs the same negotiation a real host performs.
		Plugins:          plugin.RosterHostPluginMap(opts.Manifest.Name, opts.Manifest, p.HostVersion),
		Cmd:              exec.Command(p.Path, p.Args...), //nolint:gosec // the caller names the binary under test
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
	})
	// Kill is how the provider process dies, and it runs on every path out of
	// here including the failure ones — a harness that leaked a subprocess per
	// failed run would be worse than the bug it was looking for.
	defer client.Kill()

	rpcClient, err := client.Client()
	if err != nil {
		return Report{}, cerr.New(cerr.KindUnavailable, "rosterconform.RunOutOfProcess", fmt.Errorf(
			"could not launch or dial the provider at %s: %w", p.Path, err))
	}

	raw, err := rpcClient.Dispense(plugin.RosterName)
	if err != nil {
		// Includes the min_host_version refusal, which is a correct outcome
		// for an incompatible pairing rather than a conformance verdict.
		return Report{}, cerr.New(cerr.KindUnsupported, "rosterconform.RunOutOfProcess", fmt.Errorf(
			"the provider at %s could not be dispensed under plugin key %q: %w", p.Path, plugin.RosterName, err))
	}

	c, ok := raw.(roster.Roster)
	if !ok {
		return Report{}, cerr.New(cerr.KindInvalid, "rosterconform.RunOutOfProcess", fmt.Errorf(
			"the value dispensed for %q is a %T, which does not implement roster.Roster", plugin.RosterName, raw))
	}

	rep, err := Run(ctx, c, opts)
	if err != nil {
		return rep, err
	}
	// Recorded only here, because only here is it known. See
	// [Report.Transport].
	rep.Transport = fmt.Sprintf("%s (%s)", TransportOutOfProcess, p.Path)
	return rep, nil
}
