package arqtossdk_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every class interface embeds connector.Connector, which declares Close() error.
// So every implementer writes a Close — but only some class SERVICES bind it as an
// RPC, and an implementer cannot tell which from the interface.
//
// ⚠️ Measured: of the three class services with wire bindings, TWO bind rpc Close
// (roster, authenticator) and ONE does not (credentialloader). A wipe written in a
// CredentialLoader's Close therefore does not run out of process, while the same
// code in a Roster's Close does.
//
// That asymmetry is deliberate and it is documented at the host stub
// (plugin/plugin.go's grpcClient.Close). What it was NOT documented in is the
// credential package itself — where a CONNECTOR AUTHOR reads, as opposed to the
// stub where a HOST author reads — and the credential package doc promises material
// is "wipeable (dies-with-session)", which is precisely the expectation a
// Close-based wipe silently fails to meet (arqtiqa/arqtos-sdk-go#89).
//
// ⚠️ THIS TEST IS THE CONTROL FOR THAT DOCUMENTATION. The statement in the
// credential package is only true while credentialloader.proto binds no Close, and
// prose cannot notice when that stops being true. If the RPC is added — which is a
// legitimate outcome and the other half of #89 — this test fails, and the same
// change must then correct the documentation instead of leaving it confidently
// wrong.
func TestCloseBinding_OnlyTheServicesThatBindItAreClaimedTo(t *testing.T) {
	// bindsClose is the measured state, not an aspiration. A service added here
	// without a decision about Close will fail rather than default.
	want := map[string]bool{
		"roster.proto":           true,
		"authenticator.proto":    true,
		"credentialloader.proto": false,
	}

	dir := filepath.Join("proto", "connector", "v1")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".proto") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		text := string(body)
		// connector.proto carries the SHARED messages (CloseRequest/CloseResponse)
		// and declares no service, so it is not a class service.
		if !strings.Contains(text, "service ") {
			continue
		}
		seen[e.Name()] = strings.Contains(text, "rpc Close")
	}

	if len(seen) == 0 {
		t.Fatal("no class service proto was found, so this control examines nothing")
	}
	// ⚠️ Both directions. An unexpected service is as much a finding as a changed
	// binding: it means a class shipped a wire contract nobody decided Close for.
	var problems []string
	for name, binds := range seen {
		exp, known := want[name]
		if !known {
			problems = append(problems, name+" is a class service this control does not know — decide "+
				"whether it binds Close, then add it here")
			continue
		}
		if binds != exp {
			problems = append(problems, name+": binds rpc Close = "+boolStr(binds)+", control expects "+
				boolStr(exp))
		}
	}
	for name := range want {
		if _, ok := seen[name]; !ok {
			problems = append(problems, name+" is named by this control but is not a class service any "+
				"more — delete its line")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("the Close wire-binding state moved, and the credential package doc asserts the old "+
			"state:\n  %s\n\n⚠️ If credentialloader gained rpc Close, that is a legitimate change — but the "+
			"lifetime paragraph in credential/credential.go now says something false and must be corrected "+
			"in the same commit.", strings.Join(problems, "\n  "))
	}
	t.Logf("%d class service(s) examined; %d bind rpc Close", len(seen), countTrue(seen))
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func countTrue(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}
