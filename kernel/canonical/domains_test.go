package canonical_test

import (
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
)

// A resolved workspace configuration is digested under its own domain, so its
// result_digest can never be presented as an act, a witness or a charter
// (arqtos-sdk-go#142).
func TestDomainResolvedConfig_IsTheSixthMemberAndSeparatesFromTheOthers(t *testing.T) {
	if canonical.DomainResolvedConfig != "arqtos.resolved-config.v1" {
		t.Fatalf("DomainResolvedConfig = %q", canonical.DomainResolvedConfig)
	}
	if !canonical.DomainResolvedConfig.Valid() {
		t.Fatal("DomainResolvedConfig is declared but not Valid(); Digest would refuse it")
	}
	members := 0
	for _, d := range canonical.Domains() {
		if d == canonical.DomainResolvedConfig {
			members++
		}
	}
	if members != 1 || len(canonical.Domains()) != 7 {
		t.Fatalf("Domains() lists the resolved-config domain %d time(s) among %d; want once among seven", members, len(canonical.Domains()))
	}
	body := map[string]any{"result": "1"}
	mine, err := canonical.Digest(canonical.DomainResolvedConfig, body)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range canonical.Domains() {
		if d == canonical.DomainResolvedConfig {
			continue
		}
		other, err := canonical.Digest(d, body)
		if err != nil {
			t.Fatal(err)
		}
		if other == mine {
			t.Errorf("a resolved configuration digests identically under %s", d)
		}
	}
}

// A governed configuration record or projection body is digested under its
// own domain, so a body_digest can never be presented as a resolved artifact
// or as any kernel record (arqtos-sdk-go#144, dcn-arq-00015).
func TestDomainConfigBody_IsTheSeventhMemberAndSeparatesFromTheResolvedArtifact(t *testing.T) {
	if canonical.DomainConfigBody != "arqtos.config-body.v1" {
		t.Fatalf("DomainConfigBody = %q", canonical.DomainConfigBody)
	}
	if !canonical.DomainConfigBody.Valid() {
		t.Fatal("DomainConfigBody is declared but not Valid(); Digest would refuse it")
	}
	members := 0
	for _, d := range canonical.Domains() {
		if d == canonical.DomainConfigBody {
			members++
		}
	}
	if members != 1 || len(canonical.Domains()) != 7 {
		t.Fatalf("Domains() lists the config-body domain %d time(s) among %d; want once among seven", members, len(canonical.Domains()))
	}
	body := map[string]any{"record_id": "ws_1", "slug": "platform"}
	mine, err := canonical.Digest(canonical.DomainConfigBody, body)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := canonical.Digest(canonical.DomainResolvedConfig, body)
	if err != nil {
		t.Fatal(err)
	}
	if mine == resolved {
		t.Fatal("a record body and a resolved artifact with the same fields share one identity")
	}
}
