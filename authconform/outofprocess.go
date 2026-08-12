package authconform

import (
	"context"
	"fmt"
	"os/exec"

	goplugin "github.com/hashicorp/go-plugin"

	"github.com/arqtiqa/arqtos-sdk-go/authenticator"
	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
)

// TransportOutOfProcess is what [Report.Transport] records for a run that
// crossed a real gRPC boundary.
const TransportOutOfProcess = "out-of-process"

// A Provider names an out-of-process (Track-B) Authenticator provider to launch
// and dial.
type Provider struct {
	// Path is the provider EXECUTABLE. Build it first — this launches a
	// binary, it does not compile one.
	Path string
	// Args are passed to the executable. Most providers need none: a go-plugin
	// provider is configured by its environment and its own config, not by
	// argv.
	Args []string
	// HostVersion is the contract version the host doing the dialling
	// implements, and it is REQUIRED: it is one half of the min_host_version
	// negotiation, and a run that skipped it would be proving conformance of a
	// pairing a real host would have refused.
	HostVersion string
}

// RunOutOfProcess launches the provider named by p, dials it over
// go-plugin/gRPC exactly as a host does, and runs every check in [Run] against
// the dispensed host stub.
//
// # Why an in-process run is not enough for this class
//
// A conformance run that never serialised anything cannot see a marshalling
// bug, and for this class the marshalling failures are the dangerous ones —
// because proto3 does not put a false bool or an empty string on the wire at
// all:
//
//   - active = false is the default, so a verified-but-DISABLED principal is
//     exactly the value a careless mapping loses, in either direction;
//   - authenticated = false is the default, so a rejection that should have
//     been a status error arrives as a perfectly-shaped anonymous assertion;
//   - an empty authorization_url or handle is indistinguishable from one the
//     provider forgot to set.
//
// Every one of those is invisible to an in-process run of the same connector,
// and every one of them ends in a session started for the wrong person, or for
// nobody, or not at all.
//
// # And the spawning has to be reachable from here
//
// A connector repository forbids os/exec in its own package tree — a connector
// must not shell out, and the ban is enforced by a test that recurses into the
// command directories too — so a connector's own CI cannot spawn its own
// binary. The exec lives on the SDK side of that boundary, once, where it is
// the harness doing it rather than the connector.
//
// # What the returned error means
//
// It is non-nil when the run could not be CARRIED OUT: no path, no host
// version, a binary that will not launch, or a provider the min_host_version
// negotiation refused. That last one is not a conformance failure — a refused
// provider was never dialled, so there is nothing to report checks about, and a
// Report full of failures would misattribute a correct refusal to broken
// behaviour.
//
// A provider that runs and is non-conformant yields a nil error and a Report
// whose Err reports the failures, the same as [Run]. Gate on Report.Err.
func RunOutOfProcess(ctx context.Context, p Provider, opts Options) (Report, error) {
	const op = "authconform.RunOutOfProcess"

	if p.Path == "" {
		return Report{}, cerr.New(cerr.KindInvalid, op,
			fmt.Errorf("Provider.Path is unset: this launches a provider executable, it does not build one"))
	}
	if p.HostVersion == "" {
		return Report{}, cerr.New(cerr.KindInvalid, op, fmt.Errorf(
			"Provider.HostVersion is unset, so the provider's min_host_version cannot be negotiated; a run "+
				"that skips the negotiation proves conformance of a pairing a real host would have refused"))
	}

	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: plugin.Handshake,
		// The HOST-side map: it carries the manifest and this host's version,
		// so Dispense performs the same negotiation a real host performs.
		Plugins:          plugin.AuthenticatorHostPluginMap(opts.Manifest.Name, opts.Manifest, p.HostVersion),
		Cmd:              exec.Command(p.Path, p.Args...), //nolint:gosec // the caller names the binary under test
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
	})
	// Kill is how the provider process dies, and it runs on every path out of
	// here including the failure ones — a harness that leaked a subprocess per
	// failed run would be worse than the bug it was looking for.
	defer client.Kill()

	rpcClient, err := client.Client()
	if err != nil {
		return Report{}, cerr.New(cerr.KindUnavailable, op, fmt.Errorf(
			"could not launch or dial the provider at %s: %w", p.Path, err))
	}

	raw, err := rpcClient.Dispense(plugin.AuthenticatorName)
	if err != nil {
		// Includes the min_host_version refusal, which is a correct outcome for
		// an incompatible pairing rather than a conformance verdict.
		return Report{}, cerr.New(cerr.KindUnsupported, op, fmt.Errorf(
			"the provider at %s could not be dispensed under plugin key %q: %w",
			p.Path, plugin.AuthenticatorName, err))
	}

	a, ok := raw.(authenticator.Authenticator)
	if !ok {
		return Report{}, cerr.New(cerr.KindInvalid, op, fmt.Errorf(
			"the value dispensed for %q is a %T, which does not implement authenticator.Authenticator",
			plugin.AuthenticatorName, raw))
	}

	rep, err := Run(ctx, a, opts)
	if err != nil {
		return rep, err
	}
	// Recorded only here, because only here is it known. An in-process Run
	// cannot tell whether it crossed a wire, so it says so rather than
	// claiming it did.
	rep.Transport = fmt.Sprintf("%s (%s)", TransportOutOfProcess, p.Path)
	return rep, nil
}
