package arqtossdk_test

import (
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
)

// ⚠️ The encoder's actual behaviour, so the README's corrected attribution rests
// on a measurement rather than on my reading of the code.
func assertEncoderKeepsUnknownKeys(t *testing.T) {
	t.Helper()
	raw, err := canonical.Encode(map[string]any{"known": "a", "an_unknown_field": "b"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.Contains(string(raw), "an_unknown_field") {
		t.Error("canonical.Encode dropped an unknown key — if it now rejects or strips them, the " +
			"README's attribution should move back and this test should say so")
	}
}
