package mcpconform

import (
	"context"
	"net/http"
)

// Exported for this package's external test binary.
//
// The protocol-compatibility tests have to speak raw JSON-RPC over HTTP for the
// same reason [SessionIndependence] does: the SDK's Go client cannot express a
// client that skips the handshake or announces an older protocol version. They
// share one implementation deliberately — if the tests carried their own copy
// of the wire code they would pin behaviour the shipped check does not have,
// which is the failure mode this package exists to prevent.

// RPCResponse is the decoded JSON-RPC response used by the tests.
type RPCResponse = rpcResponse

// PostJSONRPC exposes postJSONRPC.
func PostJSONRPC(ctx context.Context, hc *http.Client, url string, headers map[string]string, msg map[string]any) (*http.Response, error) {
	return postJSONRPC(ctx, hc, url, headers, msg)
}

// DecodeJSONRPC exposes decodeJSONRPC.
func DecodeJSONRPC(resp *http.Response) (RPCResponse, error) { return decodeJSONRPC(resp) }

// ProbeProtocolVersion exposes the revision the handshake-less probe announces.
const ProbeProtocolVersion = probeProtocolVersion
