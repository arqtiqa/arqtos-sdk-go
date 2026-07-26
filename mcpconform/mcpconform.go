// Package mcpconform checks that an MCP server can be driven by arqtos as a
// declarative connector backend, using the official MCP Go SDK
// (github.com/modelcontextprotocol/go-sdk) as the client.
//
// A declarative connector does not ship Go code: it points arqtos at an MCP
// server and maps connector-class operations onto that server's tools. The
// server therefore has to satisfy a small number of protocol properties before
// it can be wired up at all. This package makes those properties checkable by
// the connector author, in their own CI, before arqtos ever dials the server.
//
// # The stateless assumption
//
// arqtos does not promise a long-lived MCP session. The MCP specification is
// moving to a stateless core — no server-held session, no guaranteed
// initialize handshake between calls, required values carried in HTTP headers
// rather than recovered by inspecting payloads — and the official SDK's
// streamable-HTTP handler already implements that mode
// ([mcp.StreamableHTTPOptions.Stateless]). A server whose behaviour depends on
// state accumulated earlier in a session is not usable as a connector backend
// even if it works interactively.
//
// [Run] exercises that assumption directly: it makes one connection, runs the
// per-session checks, closes it, and then makes a second, independent
// connection and requires the server to present the same tools. That is why
// [Run] takes a transport *factory* rather than a single [mcp.Transport] — a
// transport is consumed by a connection, and the point of the check is that a
// second connection stands on its own.
//
// # Protocol compatibility
//
// Compatibility across protocol versions is the SDK's job, not arqtos's: the
// SDK's streamable-HTTP wrapper serves a previous-protocol client and a
// stateless client from the same handler. arqtos deliberately ships no
// dual-protocol shim of its own. The tests in this package pin that behaviour
// at the JSON-RPC level so a version bump of the SDK cannot quietly change it.
//
// # Usage
//
//	report, err := mcpconform.Run(ctx, mcpconform.StreamableHTTP(endpoint), &mcpconform.Options{
//		RequireTools: []string{"list_items", "create_item"},
//	})
//	if err != nil {
//		return err // the check could not be run at all
//	}
//	if err := report.Err(); err != nil {
//		return err // the server ran, and is not conformant
//	}
package mcpconform

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// Check names reported by [Run]. They are stable identifiers: a caller may
// switch on them, and a CI job may allowlist a known failure by name.
const (
	// CheckInitialize covers connecting and negotiating a protocol version.
	CheckInitialize = "initialize"
	// CheckPing covers a ping round-trip on the negotiated session.
	CheckPing = "ping"
	// CheckListTools covers tools/list returning at least one tool.
	CheckListTools = "tools/list"
	// CheckRequiredTools covers Options.RequireTools being present in
	// tools/list. It is only reported when RequireTools is non-empty.
	CheckRequiredTools = "tools/required"
	// CheckStatelessReconnect covers a second, independent connection
	// presenting the same tools as the first — the property arqtos relies on
	// when it does not hold a session open.
	CheckStatelessReconnect = "stateless-reconnect"
)

// A TransportFactory opens a fresh, unconnected transport to the server under
// test. [Run] calls it more than once and must get an independent transport
// each time; returning the same value twice makes the stateless check
// meaningless, because most transports cannot be reconnected.
type TransportFactory func() (mcp.Transport, error)

// StreamableHTTP returns a [TransportFactory] for an MCP server reachable at
// endpoint over the streamable HTTP transport.
//
// The standalone SSE stream is disabled. A stateless server answers the GET
// that would open it with 405 Method Not Allowed, and arqtos drives connector
// backends request/response — it does not consume server-initiated messages
// outside a request it made.
func StreamableHTTP(endpoint string) TransportFactory {
	return func() (mcp.Transport, error) {
		return &mcp.StreamableClientTransport{
			Endpoint:             endpoint,
			DisableStandaloneSSE: true,
		}, nil
	}
}

// StreamableHTTPWithClient is [StreamableHTTP] with a caller-supplied HTTP
// client, for endpoints that need custom timeouts, transport-level auth, or a
// test server's client.
func StreamableHTTPWithClient(endpoint string, hc *http.Client) TransportFactory {
	return func() (mcp.Transport, error) {
		return &mcp.StreamableClientTransport{
			Endpoint:             endpoint,
			HTTPClient:           hc,
			DisableStandaloneSSE: true,
		}, nil
	}
}

// Options tunes a conformance run. A nil *Options is valid and means defaults.
type Options struct {
	// Client identifies the conformance client to the server under test. If
	// nil, a default identity is sent.
	Client *mcp.Implementation

	// RequireTools names tools the server must expose. When empty, [Run] only
	// requires that at least one tool exists.
	RequireTools []string
}

func (o *Options) client() *mcp.Implementation {
	if o != nil && o.Client != nil {
		return o.Client
	}
	return &mcp.Implementation{Name: "arqtos-mcpconform", Version: "1"}
}

func (o *Options) requireTools() []string {
	if o == nil {
		return nil
	}
	return o.RequireTools
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
	// ProtocolVersion is the version the server negotiated, or "" if the
	// connection never got that far.
	ProtocolVersion string
	// Server is the server's self-reported identity, or nil if unknown.
	Server *mcp.Implementation
	// Results holds one entry per check that was run, in run order.
	Results []Result
}

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
// [cerr.KindInvalid] naming every failed check. The server responded; it is
// the server's behaviour that is wrong, which is why this is Invalid rather
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
	return cerr.New(cerr.KindInvalid, "mcpconform", fmt.Errorf("%s", strings.Join(parts, "; ")))
}

// String renders the report as one line per check, for CI logs.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "mcpconform: protocol=%s", or(r.ProtocolVersion, "<none>"))
	if r.Server != nil {
		fmt.Fprintf(&b, " server=%s/%s", or(r.Server.Name, "<unnamed>"), or(r.Server.Version, "<unversioned>"))
	}
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

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Run checks the MCP server reachable through newTransport.
//
// The returned error is non-nil only when the check could not be carried out —
// a nil factory, or a factory that itself failed. A server that answers and is
// non-conformant yields a nil error and a Report whose Err reports the
// failures; callers gate on Report.Err, not on the returned error alone.
func Run(ctx context.Context, newTransport TransportFactory, opts *Options) (Report, error) {
	if newTransport == nil {
		return Report{}, cerr.New(cerr.KindInvalid, "mcpconform.Run", fmt.Errorf("nil transport factory"))
	}

	var rep Report

	first, err := connect(ctx, newTransport, opts)
	if err != nil {
		var he *harnessError
		if errors.As(err, &he) {
			return Report{}, he.err
		}
		rep.add(CheckInitialize, false, err.Error())
		return rep, nil
	}
	defer first.Close()

	if init := first.InitializeResult(); init != nil {
		rep.ProtocolVersion = init.ProtocolVersion
		rep.Server = init.ServerInfo
	}
	rep.add(CheckInitialize, true, "negotiated "+or(rep.ProtocolVersion, "<none>"))

	if err := first.Ping(ctx, nil); err != nil {
		rep.add(CheckPing, false, err.Error())
	} else {
		rep.add(CheckPing, true, "")
	}

	firstTools, err := toolNames(ctx, first)
	switch {
	case err != nil:
		rep.add(CheckListTools, false, err.Error())
	case len(firstTools) == 0:
		rep.add(CheckListTools, false, "server exposes no tools; a declarative connector maps its operations onto tools")
	default:
		rep.add(CheckListTools, true, fmt.Sprintf("%d tool(s)", len(firstTools)))
	}

	if required := opts.requireTools(); len(required) > 0 {
		var missing []string
		for _, name := range required {
			if !slices.Contains(firstTools, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			rep.add(CheckRequiredTools, false, "missing tool(s): "+strings.Join(missing, ", "))
		} else {
			rep.add(CheckRequiredTools, true, "")
		}
	}

	// Close the first session before opening the second: the point of the
	// check is that the second connection does not inherit anything.
	// ClientSession.Close is idempotent, so the deferred close above stays.
	_ = first.Close()

	second, err := connect(ctx, newTransport, opts)
	if err != nil {
		var he *harnessError
		if errors.As(err, &he) {
			// The factory, not the server, failed. Hand back the checks that
			// did run along with the harness error, so a misconfigured run is
			// never mistaken for a non-conformant server.
			return rep, he.err
		}
		rep.add(CheckStatelessReconnect, false, "second, independent connection failed: "+err.Error())
		return rep, nil
	}
	defer second.Close()

	secondTools, err := toolNames(ctx, second)
	switch {
	case err != nil:
		rep.add(CheckStatelessReconnect, false, "tools/list on a second, independent connection failed: "+err.Error())
	case !slices.Equal(firstTools, secondTools):
		rep.add(CheckStatelessReconnect, false, fmt.Sprintf(
			"tool set depends on session state: first connection saw [%s], second saw [%s]",
			strings.Join(firstTools, " "), strings.Join(secondTools, " ")))
	default:
		rep.add(CheckStatelessReconnect, true, "")
	}

	return rep, nil
}

func (r *Report) add(name string, pass bool, detail string) {
	r.Results = append(r.Results, Result{Name: name, Pass: pass, Detail: detail})
}

// harnessError marks a failure of the harness itself (as opposed to a failure
// of the server under test), so Run can return it rather than record it.
type harnessError struct{ err error }

func (e *harnessError) Error() string { return e.err.Error() }

func (e *harnessError) Unwrap() error { return e.err }

func connect(ctx context.Context, newTransport TransportFactory, opts *Options) (*mcp.ClientSession, error) {
	t, err := newTransport()
	if err != nil {
		return nil, &harnessError{cerr.New(cerr.KindInvalid, "mcpconform.Run", fmt.Errorf("transport factory: %w", err))}
	}
	if t == nil {
		return nil, &harnessError{cerr.New(cerr.KindInvalid, "mcpconform.Run", fmt.Errorf("transport factory returned nil transport"))}
	}
	return mcp.NewClient(opts.client(), nil).Connect(ctx, t, nil)
}

// toolNames lists every tool the server exposes, following pagination, and
// returns the names sorted so two connections can be compared directly.
func toolNames(ctx context.Context, cs *mcp.ClientSession) ([]string, error) {
	var names []string
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if t != nil {
			names = append(names, t.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}
