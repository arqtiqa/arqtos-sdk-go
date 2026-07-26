package mcpconform_test

// These tests pin, at the JSON-RPC/HTTP level, the compatibility arqtos takes
// from the official MCP SDK's streamable-HTTP wrapper instead of implementing
// a dual-protocol shim of its own. They speak raw HTTP rather than using the
// SDK's Go client on purpose: the Go client always announces the SDK's latest
// protocol version (the override is unexported), so a previous-protocol client
// cannot be expressed through it. A connector author's server will be reached
// by clients arqtos does not control, and this is what those clients look like
// on the wire.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/mcpconform"
)

// Protocol versions as they appear on the wire. Kept as literals rather than
// referenced from the SDK: the SDK's own constants are unexported, and pinning
// literals is the point — if the set the SDK supports changes, these tests
// say so.
const (
	protocol20241105 = "2024-11-05"
	protocol20250326 = "2025-03-26"
	protocol20250618 = "2025-06-18"
	protocol20251125 = "2025-11-25"
	// protocol20260630 is the stateless-core spec revision. The SDK v1.6.0
	// knows the constant (it gates the standard Mcp-Method / Mcp-Name request
	// headers on it) but does not yet accept it during negotiation. See
	// TestStreamableHandler_StatelessSpecVersionIsNotYetNegotiable.
	protocol20260630 = "2026-06-30"
)

// post and readRPC are thin t.Fatalf wrappers over the package's own wire code
// (mcpconform.PostJSONRPC / DecodeJSONRPC, exported for tests in
// export_test.go). They are deliberately not a second implementation: the
// session-independence check speaks the wire through exactly this code, so
// these tests pin what the shipped check does rather than a lookalike.

// post sends a single JSON-RPC message to an MCP streamable-HTTP endpoint.
func post(t *testing.T, url string, headers map[string]string, msg map[string]any) *http.Response {
	t.Helper()
	resp, err := mcpconform.PostJSONRPC(t.Context(), nil, url, headers, msg)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// readRPC decodes the first JSON-RPC message from a response, handling both
// framings the handler can use: a plain JSON body, and an SSE stream (the
// default) whose payload rides in a "data:" line.
func readRPC(t *testing.T, resp *http.Response) mcpconform.RPCResponse {
	t.Helper()
	rpc, err := mcpconform.DecodeJSONRPC(resp)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return rpc
}

func mustResult(t *testing.T, resp *http.Response, into any) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	rpc := readRPC(t, resp)
	if rpc.Error != nil {
		t.Fatalf("JSON-RPC error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	if into == nil {
		return
	}
	if err := json.Unmarshal(rpc.Result, into); err != nil {
		t.Fatalf("decode result %q: %v", rpc.Result, err)
	}
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type toolsListResult struct {
	Tools []struct {
		Name string `json:"name"`
	} `json:"tools"`
}

func toolNamesOf(r toolsListResult) []string {
	out := make([]string, 0, len(r.Tools))
	for _, tool := range r.Tools {
		out = append(out, tool.Name)
	}
	return out
}

// AC: a previous-protocol client and a stateless-protocol client both connect
// through the SDK's wrapper — the same handler instance, no arqtos shim.
func TestStatelessWrapper_ServesBothPreviousAndStatelessClients(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("compat-fixture", "list_items"), true)

	t.Run("previous protocol client, with handshake", func(t *testing.T) {
		// A 2025-03-26 client: it performs the initialize handshake and
		// expects the server to negotiate down to its version.
		var init initializeResult
		mustResult(t, post(t, url, nil, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "initialize",
			"params": map[string]any{
				"protocolVersion": protocol20250326,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "previous-protocol-client", "version": "1"},
			},
		}), &init)
		if init.ProtocolVersion != protocol20250326 {
			t.Fatalf("negotiated protocolVersion = %q, want %q", init.ProtocolVersion, protocol20250326)
		}

		var tools toolsListResult
		mustResult(t, post(t, url, map[string]string{"Mcp-Protocol-Version": protocol20250326}, map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/list",
		}), &tools)
		if got := toolNamesOf(tools); len(got) != 1 || got[0] != "list_items" {
			t.Fatalf("tools = %v, want [list_items]", got)
		}
	})

	t.Run("stateless client, no handshake", func(t *testing.T) {
		// The stateless core removes the session and the initialize
		// handshake. This client sends neither, and never carries a session
		// id, yet must be served by the same handler.
		var tools toolsListResult
		mustResult(t, post(t, url, map[string]string{"Mcp-Protocol-Version": protocol20251125}, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/list",
		}), &tools)
		if got := toolNamesOf(tools); len(got) != 1 || got[0] != "list_items" {
			t.Fatalf("tools = %v, want [list_items]", got)
		}

		resp := post(t, url, map[string]string{"Mcp-Protocol-Version": protocol20251125}, map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "list_items",
				"arguments": map[string]any{"text": "hello"},
			},
		})
		mustResult(t, resp, nil)
		if got := resp.Header.Get("Mcp-Session-Id"); got != "" {
			// Not a failure of the spec, but arqtos must not come to depend
			// on it: a stateless call is answered without a session to reuse.
			t.Logf("stateless tools/call returned Mcp-Session-Id %q", got)
		}
	})
}

// Statelessness has to be configured; it is not what you get by default.
//
// The default handler mints a session id for a request that arrives without
// one, so such a request is *not* treated as stateless: a client that skips
// the handshake is told the call is invalid during initialization. Anyone
// standing up an arqtos MCP surface must therefore set
// StreamableHTTPOptions.Stateless explicitly — reasoning "we never keep
// session state, so we are stateless" is not enough.
func TestDefaultWrapper_RejectsClientThatSkipsHandshake(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("stateful-compat-fixture", "list_items"), false)

	resp := post(t, url, map[string]string{"Mcp-Protocol-Version": protocol20251125}, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the refusal is a JSON-RPC error, not an HTTP one)", resp.StatusCode)
	}
	rpc := readRPC(t, resp)
	if rpc.Error == nil {
		t.Fatalf("a handshake-less call succeeded against a non-stateless handler; result: %s", rpc.Result)
	}
	if !strings.Contains(rpc.Error.Message, "during session initialization") {
		t.Errorf("error message = %q, want it to name session initialization", rpc.Error.Message)
	}
}

// With no Mcp-Protocol-Version header at all the server falls back to
// 2025-03-26, the last revision that predates the header. A pre-header client
// therefore still works.
func TestStreamableHandler_WhenProtocolHeaderAbsent_ServesPreHeaderClient(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("no-header-fixture", "list_items"), true)

	var tools toolsListResult
	mustResult(t, post(t, url, nil, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}), &tools)
	if got := toolNamesOf(tools); len(got) != 1 {
		t.Fatalf("tools = %v, want one tool", got)
	}
}

// The handshake-less probe in SessionIndependence announces a fixed revision.
// It has to be one the acceptance table below shows is negotiable, or the probe
// would draw a 400 that has nothing to do with session dependence. Pinning the
// two together means the SDK dropping the revision surfaces here, next to the
// evidence, rather than as a mystery conformance failure in a connector's CI.
func TestProbeProtocolVersion_IsARevisionTheHandlerAccepts(t *testing.T) {
	t.Parallel()

	if mcpconform.ProbeProtocolVersion != protocol20251125 {
		t.Fatalf("probe announces %q, but this file pins %q as the newest negotiable revision",
			mcpconform.ProbeProtocolVersion, protocol20251125)
	}
}

func TestStreamableHandler_ProtocolVersionHeaderAcceptance(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("versions-fixture", "list_items"), true)

	for _, tc := range []struct {
		version string
		want    int
	}{
		{protocol20241105, http.StatusOK},
		{protocol20250326, http.StatusOK},
		{protocol20250618, http.StatusOK},
		{protocol20251125, http.StatusOK},
		{"1999-01-01", http.StatusBadRequest},
	} {
		t.Run(tc.version, func(t *testing.T) {
			resp := post(t, url, map[string]string{"Mcp-Protocol-Version": tc.version}, map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "tools/list",
			})
			if resp.StatusCode != tc.want {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, tc.want, body)
			}
		})
	}
}

// Tripwire. The SDK at the version this module pins implements the
// stateless-core request headers (Mcp-Method / Mcp-Name) and gates them on
// protocol >= 2026-06-30, but does not list 2026-06-30 as negotiable — so a
// client announcing it is rejected before those headers are ever validated.
//
// When this test starts failing, the SDK has begun accepting the
// stateless-core revision. That is the moment to re-check arqtos's MCP
// surfaces against the header requirements, not a reason to delete the test.
func TestStreamableHandler_StatelessSpecVersionIsNotYetNegotiable(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("future-fixture", "list_items"), true)

	resp := post(t, url, map[string]string{"Mcp-Protocol-Version": protocol20260630}, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d for protocol %s, want %d.\n"+
			"If this is now 200, the pinned MCP SDK negotiates the stateless-core revision: "+
			"re-verify the Mcp-Method / Mcp-Name request headers against arqtos MCP surfaces "+
			"before relaxing this test.\nbody: %s",
			resp.StatusCode, protocol20260630, http.StatusBadRequest, body)
	}
}

// A stateless handler offers no server-initiated stream, so the GET that would
// open one is refused. An arqtos module must not assume it can receive
// out-of-band notifications from a connector backend.
func TestStatelessHandler_RefusesStandaloneSSEStream(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("get-fixture", "list_items"), true)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	if got := resp.Header.Get("Allow"); got != "POST" {
		t.Errorf("Allow = %q, want %q", got, "POST")
	}
}
