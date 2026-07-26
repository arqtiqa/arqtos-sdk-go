package mcpconform

// Raw JSON-RPC over streamable HTTP.
//
// Not everything this package has to probe can be expressed through the SDK's
// Go client. That client always performs the initialize handshake, and always
// announces the SDK's own protocol version (the override is unexported). The
// stateless core describes a client that does neither — it POSTs a method call
// with no handshake behind it and no session to belong to. To check that a
// server serves such a client, this package has to *be* one, on the wire.
//
// The protocol-compatibility tests alongside this package speak the same wire
// through the same code (see export_test.go): one implementation, so a test
// cannot pin behaviour the check does not actually have.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// headerProtocolVersion is the MCP protocol-version request header.
const headerProtocolVersion = "Mcp-Protocol-Version"

// probeProtocolVersion is the revision the handshake-less probe announces. It
// is the newest revision the pinned SDK negotiates; the stateless-core
// revision (2026-06-30) is not yet negotiable, which
// TestStreamableHandler_StatelessSpecVersionIsNotYetNegotiable pins. A server
// that does not recognise this value answers 400, and the probe retries with
// no version header rather than blaming the server for session dependence.
const probeProtocolVersion = "2025-11-25"

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// String renders the error for a check Detail. The code is only shown when the
// server set one — the SDK sends 0 for its session-initialization refusal, and
// "JSON-RPC error 0" reads like a defect in the checker rather than the
// server's own words.
func (e *rpcError) String() string {
	if e.Code == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// postJSONRPC sends a single JSON-RPC message to an MCP streamable-HTTP
// endpoint. A nil hc means [http.DefaultClient]. The caller closes the returned
// response body.
//
// It never sets Mcp-Session-Id: a probe that carried one would be asking a
// different question.
func postJSONRPC(ctx context.Context, hc *http.Client, url string, headers map[string]string, msg map[string]any) (*http.Response, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(req)
}

// decodeJSONRPC reads the first JSON-RPC message out of a response, handling
// both framings the handler can use: a plain JSON body, and an SSE stream (the
// default) whose payload rides in a "data:" line. It consumes the body; closing
// it remains the caller's job.
func decodeJSONRPC(resp *http.Response) (rpcResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return rpcResponse{}, fmt.Errorf("read body: %w", err)
	}
	raw := body
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		raw = nil
		sc := bufio.NewScanner(bytes.NewReader(body))
		for sc.Scan() {
			if after, ok := strings.CutPrefix(sc.Text(), "data:"); ok {
				raw = []byte(strings.TrimSpace(after))
				break
			}
		}
		if raw == nil {
			return rpcResponse{}, fmt.Errorf("no data frame in SSE response: %q", body)
		}
	}
	var out rpcResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return rpcResponse{}, fmt.Errorf("decode JSON-RPC response %q: %w", raw, err)
	}
	return out, nil
}
