package mcpconform_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/mcpconform"
)

type echoInput struct {
	Text string `json:"text"`
}

type echoOutput struct {
	Text string `json:"text"`
}

// newFixtureServer builds a synthetic MCP server exposing the named tools.
// The tool bodies are deliberately trivial: this package checks protocol
// behaviour, not tool semantics.
func newFixtureServer(name string, toolNames ...string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: name, Version: "0.1.0"}, nil)
	for _, tool := range toolNames {
		mcp.AddTool(s, &mcp.Tool{Name: tool, Description: "echoes its input"},
			func(_ context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, echoOutput, error) {
				return nil, echoOutput{Text: in.Text}, nil
			})
	}
	return s
}

// serve starts an httptest server for the given MCP server. Stateless selects
// the SDK's stateless streamable-HTTP mode.
func serve(t *testing.T, s *mcp.Server, stateless bool) string {
	t.Helper()
	return serveFunc(t, func(*http.Request) *mcp.Server { return s }, stateless)
}

func serveFunc(t *testing.T, getServer func(*http.Request) *mcp.Server, stateless bool) string {
	t.Helper()
	h := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: stateless})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL
}

func names(rs []mcpconform.Result) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}

func result(t *testing.T, rep mcpconform.Report, name string) mcpconform.Result {
	t.Helper()
	for _, r := range rep.Results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("report has no check %q; checks run: %v", name, names(rep.Results))
	return mcpconform.Result{}
}

func TestRun_WhenServerIsStateless_AllChecksPass(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("stateless-fixture", "list_items", "create_item"), true)

	rep, err := mcpconform.Run(t.Context(), mcpconform.StreamableHTTP(url), &mcpconform.Options{
		RequireTools: []string{"list_items", "create_item"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected a conformant server, got failures:\n%s", rep)
	}
	for _, want := range []string{
		mcpconform.CheckInitialize,
		mcpconform.CheckPing,
		mcpconform.CheckListTools,
		mcpconform.CheckRequiredTools,
		mcpconform.CheckStatelessReconnect,
	} {
		result(t, rep, want)
	}
	if rep.ProtocolVersion == "" {
		t.Error("expected a negotiated protocol version in the report")
	}
	if rep.Server == nil || rep.Server.Name != "stateless-fixture" {
		t.Errorf("expected the server identity in the report, got %+v", rep.Server)
	}
	if err := rep.Err(); err != nil {
		t.Errorf("Report.Err() = %v, want nil", err)
	}
}

// The same checks must pass against a session-holding server: arqtos does not
// require a connector backend to be stateless, only that it not *depend* on a
// session it cannot be guaranteed.
func TestRun_WhenServerHoldsSessions_AllChecksPass(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("stateful-fixture", "list_items"), false)

	rep, err := mcpconform.Run(t.Context(), mcpconform.StreamableHTTP(url), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected a conformant server, got failures:\n%s", rep)
	}
	// RequireTools was not set, so that check must not appear.
	for _, r := range rep.Results {
		if r.Name == mcpconform.CheckRequiredTools {
			t.Errorf("did not expect check %q without Options.RequireTools", r.Name)
		}
	}
}

// The check that earns the package: a server whose tool set is only correct
// inside the first session is rejected.
func TestRun_WhenToolSetDependsOnSession_StatelessReconnectFails(t *testing.T) {
	t.Parallel()

	var sessions atomic.Int64
	url := serveFunc(t, func(*http.Request) *mcp.Server {
		if sessions.Add(1) == 1 {
			return newFixtureServer("drifting-fixture", "list_items")
		}
		return newFixtureServer("drifting-fixture", "a_different_tool")
	}, false)

	rep, err := mcpconform.Run(t.Context(), mcpconform.StreamableHTTP(url), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.OK() {
		t.Fatalf("expected the stateless-reconnect check to fail, report:\n%s", rep)
	}
	got := result(t, rep, mcpconform.CheckStatelessReconnect)
	if got.Pass {
		t.Fatalf("check %q passed; report:\n%s", got.Name, rep)
	}
	if cerr.KindOf(rep.Err()) != cerr.KindInvalid {
		t.Errorf("Report.Err() kind = %v, want %v", cerr.KindOf(rep.Err()), cerr.KindInvalid)
	}
}

func TestRun_WhenRequiredToolIsMissing_RequiredToolsFails(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("sparse-fixture", "list_items"), true)

	rep, err := mcpconform.Run(t.Context(), mcpconform.StreamableHTTP(url), &mcpconform.Options{
		RequireTools: []string{"list_items", "create_item"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := result(t, rep, mcpconform.CheckRequiredTools)
	if got.Pass {
		t.Fatalf("expected %q to fail; report:\n%s", got.Name, rep)
	}
	if got.Detail == "" {
		t.Error("expected a Detail naming the missing tool")
	}
	if rep.OK() {
		t.Error("Report.OK() = true, want false")
	}
}

func TestRun_WhenServerExposesNoTools_ListToolsFails(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("empty-fixture"), true)

	rep, err := mcpconform.Run(t.Context(), mcpconform.StreamableHTTP(url), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := result(t, rep, mcpconform.CheckListTools)
	if got.Pass {
		t.Fatalf("expected %q to fail for a server with no tools; report:\n%s", got.Name, rep)
	}
}

// An unreachable endpoint is the server's problem, not the harness's: Run
// returns a report whose initialize check failed, not a harness error.
func TestRun_WhenServerUnreachable_ReportsInitializeFailure(t *testing.T) {
	t.Parallel()

	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return newFixtureServer("never-served")
	}, nil)
	ts := httptest.NewServer(h)
	url := ts.URL
	ts.Close() // the endpoint is now dead

	rep, err := mcpconform.Run(t.Context(), mcpconform.StreamableHTTP(url), nil)
	if err != nil {
		t.Fatalf("Run returned a harness error for an unreachable server: %v", err)
	}
	got := result(t, rep, mcpconform.CheckInitialize)
	if got.Pass {
		t.Fatalf("expected %q to fail against a dead endpoint; report:\n%s", got.Name, rep)
	}
	if len(rep.Results) != 1 {
		t.Errorf("expected the run to stop after initialize, got checks %v", names(rep.Results))
	}
}

// A broken factory is the harness's problem, and is reported as an error
// rather than as a failed check, so a caller never mistakes a misconfigured
// run for a non-conformant server.
func TestRun_WhenTransportFactoryFails_ReturnsHarnessError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		factory mcpconform.TransportFactory
	}{
		{"nil factory", nil},
		{"nil transport", func() (mcp.Transport, error) { return nil, nil }},
		{"factory error", func() (mcp.Transport, error) { return nil, context.DeadlineExceeded }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep, err := mcpconform.Run(t.Context(), tc.factory, nil)
			if err == nil {
				t.Fatalf("Run returned nil error; report:\n%s", rep)
			}
			if cerr.KindOf(err) != cerr.KindInvalid {
				t.Errorf("kind = %v, want %v", cerr.KindOf(err), cerr.KindInvalid)
			}
			if len(rep.Results) != 0 {
				t.Errorf("expected an empty report, got %v", names(rep.Results))
			}
		})
	}
}

func TestStreamableHTTPWithClient_UsesSuppliedClient(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("client-fixture", "list_items"), true)

	var calls atomic.Int64
	hc := &http.Client{Transport: countingTransport{n: &calls, next: http.DefaultTransport}}

	rep, err := mcpconform.Run(t.Context(), mcpconform.StreamableHTTPWithClient(url, hc), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected a conformant server, got:\n%s", rep)
	}
	if calls.Load() == 0 {
		t.Error("the supplied *http.Client was never used")
	}
}

type countingTransport struct {
	n    *atomic.Int64
	next http.RoundTripper
}

func (c countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.next.RoundTrip(r)
}

// A factory that works once and then breaks is the harness's fault, not the
// server's, even though four checks already passed.
func TestRun_WhenSecondConnectionFactoryFails_ReturnsHarnessErrorWithPartialReport(t *testing.T) {
	t.Parallel()

	url := serve(t, newFixtureServer("flaky-factory-fixture", "list_items"), true)

	var calls atomic.Int64
	factory := func() (mcp.Transport, error) {
		if calls.Add(1) == 1 {
			f, _ := mcpconform.StreamableHTTP(url)()
			return f, nil
		}
		return nil, context.DeadlineExceeded
	}

	rep, err := mcpconform.Run(t.Context(), factory, nil)
	if err == nil {
		t.Fatalf("expected a harness error; report:\n%s", rep)
	}
	if cerr.KindOf(err) != cerr.KindInvalid {
		t.Errorf("kind = %v, want %v", cerr.KindOf(err), cerr.KindInvalid)
	}
	if len(rep.Results) == 0 {
		t.Error("expected the checks that did run to be reported")
	}
	for _, r := range rep.Results {
		if r.Name == mcpconform.CheckStatelessReconnect {
			t.Errorf("a harness failure must not be recorded as check %q", r.Name)
		}
	}
}
