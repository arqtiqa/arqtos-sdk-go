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
// [Run] exercises that assumption with two distinct checks, because it is two
// distinct properties:
//
//   - [CheckSessionIndependent] POSTs a bare tools/list — no initialize, no
//     Mcp-Session-Id — and requires a non-error result. This is the stateless
//     core itself. It cannot be checked through the SDK's Go client, which
//     always performs the handshake, so [SessionIndependence] speaks raw HTTP.
//   - [CheckToolsStableAcrossReconnect] opens a second, independent connection
//     and requires the server to present the same tools. That is why [Run]
//     takes a transport *factory* rather than a single [mcp.Transport] — a
//     transport is consumed by a connection.
//
// The second does not imply the first. Two handshaken sessions agreeing about
// the tool set says nothing about whether the server needs the handshake, and
// an earlier version of this package let a session-holding server score a clean
// sweep on exactly the property it fails.
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// CheckSessionIndependent covers the stateless core itself: a tools/list
	// that carries no initialize handshake and no Mcp-Session-Id is answered
	// with a result rather than refused. See [SessionIndependence].
	CheckSessionIndependent = "session-independent"
	// CheckToolsStableAcrossReconnect covers a second, independent connection
	// presenting the same tools as the first, so a caller that reconnects
	// between calls sees one tool set rather than a per-session one.
	//
	// It is deliberately *not* named for statelessness: both connections
	// perform the handshake, so this check cannot detect a server that
	// requires one. [CheckSessionIndependent] is the check for that.
	CheckToolsStableAcrossReconnect = "tools-stable-across-reconnect"
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

// SessionIndependence checks the property the stateless core actually names:
// that the MCP server at endpoint answers a tools/list which carries no
// initialize handshake and no Mcp-Session-Id. A nil hc means
// [http.DefaultClient].
//
// This is the one check that cannot be made through the SDK's Go client. That
// client always performs initialize, so every connection it opens is a
// handshaken session — a check built on it can only ever compare sessions to
// each other. This function POSTs the JSON-RPC call directly, which is what a
// stateless client looks like on the wire and what arqtos does when it has no
// session to reuse.
//
// It is exported so a connector author can run it on its own, against a
// deployed endpoint, without the rest of a [Run].
//
// A server built with the SDK's streamable-HTTP handler passes this only when
// it was constructed with mcp.StreamableHTTPOptions{Stateless: true}. The
// default handler mints a session id for a request that arrives without one and
// then refuses the call as "invalid during session initialization" — so
// "we keep no state, therefore we are stateless" does not pass, and is not
// meant to.
func SessionIndependence(ctx context.Context, endpoint string, hc *http.Client) Result {
	fail := func(format string, args ...any) Result {
		return Result{Name: CheckSessionIndependent, Detail: fmt.Sprintf(format, args...)}
	}

	// No initialize before it, no Mcp-Session-Id on it. That is the whole probe.
	msg := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}

	resp, err := postJSONRPC(ctx, hc, endpoint, map[string]string{headerProtocolVersion: probeProtocolVersion}, msg)
	if err != nil {
		return fail("handshake-less tools/list could not be sent: %v", err)
	}
	if resp.StatusCode == http.StatusBadRequest {
		// The server does not recognise the announced revision. That is a
		// version-negotiation refusal, not session dependence, and reporting
		// it as the latter would blame the server for something it was never
		// asked. Re-probe with no version header, where a handler falls back
		// to the last revision that predates it.
		_ = resp.Body.Close()
		resp, err = postJSONRPC(ctx, hc, endpoint, nil, msg)
		if err != nil {
			return fail("handshake-less tools/list could not be sent: %v", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fail("handshake-less tools/list answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	rpc, err := decodeJSONRPC(resp)
	if err != nil {
		return fail("handshake-less tools/list: %v", err)
	}
	if rpc.Error != nil {
		return fail("the server refused a tools/list carrying no initialize handshake and no Mcp-Session-Id: %s. "+
			"It depends on a session arqtos does not promise. "+
			"Serving with the Go SDK, set mcp.StreamableHTTPOptions{Stateless: true}.",
			rpc.Error)
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(rpc.Result, &listed); err != nil {
		return fail("handshake-less tools/list returned a result that is not a tool list: %v", err)
	}
	return Result{
		Name:   CheckSessionIndependent,
		Pass:   true,
		Detail: fmt.Sprintf("%d tool(s) listed with no handshake and no session id", len(listed.Tools)),
	}
}

// sessionIndependence runs [SessionIndependence] against the endpoint the given
// transport talks to.
//
// A transport that is not streamable HTTP has no endpoint to POST to, so the
// check reports "not verified" — a failure, not an omission. Silently dropping
// the core check for such a transport would put back the hole this check was
// added to close: a report that is green because nothing looked.
func sessionIndependence(ctx context.Context, t mcp.Transport) Result {
	st, ok := t.(*mcp.StreamableClientTransport)
	if !ok || st.Endpoint == "" {
		return Result{
			Name: CheckSessionIndependent,
			Detail: fmt.Sprintf("not verified: session independence is probed over streamable HTTP, "+
				"and the transport factory returned %T", t),
		}
	}
	return SessionIndependence(ctx, st.Endpoint, st.HTTPClient)
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

	first, firstTransport, err := connect(ctx, newTransport, opts)
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

	firstTools, listErr := toolNames(ctx, first)
	switch {
	case listErr != nil:
		rep.add(CheckListTools, false, listErr.Error())
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
		switch {
		case listErr != nil:
			// Reporting every required tool as "missing" here would blame the
			// server for tools it was never asked about.
			rep.add(CheckRequiredTools, false, "not verified: tools/list failed")
		case len(missing) > 0:
			rep.add(CheckRequiredTools, false, "missing tool(s): "+strings.Join(missing, ", "))
		default:
			rep.add(CheckRequiredTools, true, "")
		}
	}

	// The stateless core, checked before the reconnect: it needs nothing from
	// a second connection, so a factory that breaks on its second call must
	// not be able to stop the run's most important check from happening.
	rep.Results = append(rep.Results, sessionIndependence(ctx, firstTransport))

	// Close the first session before opening the second: the point of the
	// check is that the second connection does not inherit anything.
	// ClientSession.Close is idempotent, so the deferred close above stays.
	_ = first.Close()

	second, _, err := connect(ctx, newTransport, opts)
	if err != nil {
		var he *harnessError
		if errors.As(err, &he) {
			// The factory, not the server, failed. Hand back the checks that
			// did run along with the harness error, so a misconfigured run is
			// never mistaken for a non-conformant server.
			return rep, he.err
		}
		rep.add(CheckToolsStableAcrossReconnect, false, "second, independent connection failed: "+err.Error())
		return rep, nil
	}
	defer second.Close()

	secondTools, err := toolNames(ctx, second)
	switch {
	case err != nil:
		rep.add(CheckToolsStableAcrossReconnect, false, "tools/list on a second, independent connection failed: "+err.Error())
	case listErr != nil:
		// Without a first tool set there is nothing to compare against;
		// saying the sets differ would misattribute the earlier failure.
		rep.add(CheckToolsStableAcrossReconnect, false, "not verified: tools/list failed on the first connection")
	case !slices.Equal(firstTools, secondTools):
		rep.add(CheckToolsStableAcrossReconnect, false, fmt.Sprintf(
			"tool set is not stable across reconnects: first connection saw [%s], second saw [%s]",
			strings.Join(firstTools, " "), strings.Join(secondTools, " ")))
	default:
		rep.add(CheckToolsStableAcrossReconnect, true, "")
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

// connect opens one session and also hands back the transport it was opened
// on, so a check that has to address the same server outside the SDK client —
// [sessionIndependence] — can do so without asking the factory for an extra
// transport it was never promised.
func connect(ctx context.Context, newTransport TransportFactory, opts *Options) (*mcp.ClientSession, mcp.Transport, error) {
	t, err := newTransport()
	if err != nil {
		return nil, nil, &harnessError{cerr.New(cerr.KindInvalid, "mcpconform.Run", fmt.Errorf("transport factory: %w", err))}
	}
	if t == nil {
		return nil, nil, &harnessError{cerr.New(cerr.KindInvalid, "mcpconform.Run", fmt.Errorf("transport factory returned nil transport"))}
	}
	cs, err := mcp.NewClient(opts.client(), nil).Connect(ctx, t, nil)
	if err != nil {
		return nil, t, err
	}
	return cs, t, nil
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
