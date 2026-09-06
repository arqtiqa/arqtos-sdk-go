package canonical_test

import (
	"slices"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
)

// listedOnce fails unless Domains() carries d exactly once; the set's size is
// not asserted, since it grows by ratified record.
func listedOnce(t *testing.T, d canonical.Domain) {
	t.Helper()
	ds := canonical.Domains()
	if i := slices.Index(ds, d); i < 0 || slices.Contains(ds[i+1:], d) {
		t.Fatalf("Domains() does not list %s exactly once: %v", d, ds)
	}
}

// A resolved workspace configuration is digested under its own domain
// (arqtos-sdk-go#142); TestDigest_SeparatesDomains carries its separation from
// every other member.
func TestDomainResolvedConfig_IsListedOnce(t *testing.T) {
	if canonical.DomainResolvedConfig != "arqtos.resolved-config.v1" {
		t.Fatalf("DomainResolvedConfig = %q", canonical.DomainResolvedConfig)
	}
	if !canonical.DomainResolvedConfig.Valid() {
		t.Fatal("DomainResolvedConfig is declared but not Valid(); Digest would refuse it")
	}
	listedOnce(t, canonical.DomainResolvedConfig)
}

// A governed configuration record or projection body is digested under its
// own domain, so a body_digest can never be presented as a resolved artifact
// or as any kernel record (arqtos-sdk-go#144, dcn-arq-00015).
func TestDomainConfigBody_IsListedOnceAndSeparatesFromTheResolvedArtifact(t *testing.T) {
	if canonical.DomainConfigBody != "arqtos.config-body.v1" {
		t.Fatalf("DomainConfigBody = %q", canonical.DomainConfigBody)
	}
	if !canonical.DomainConfigBody.Valid() {
		t.Fatal("DomainConfigBody is declared but not Valid(); Digest would refuse it")
	}
	listedOnce(t, canonical.DomainConfigBody)
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
