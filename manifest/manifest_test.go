package manifest_test

import (
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

const validProviderManifest = `
name: placeholder-credential-loader
implements: CredentialLoader
kind: provider
schema_version: "1"
capabilities: [read, lease]
min_host_version: "0.4.0"
supports:
  rotate: "false"
auth:
  api_token: PLACEHOLDER_TOKEN
  service_ref: op://<vault>/<item>/<field>
`

func TestParseValidProviderManifest(t *testing.T) {
	d, err := manifest.Parse([]byte(validProviderManifest))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if d.Name != "placeholder-credential-loader" {
		t.Fatalf("Name = %q", d.Name)
	}
	if d.Implements != connector.ClassCredentialLoader {
		t.Fatalf("Implements = %q", d.Implements)
	}
	if d.Kind != manifest.KindProvider {
		t.Fatalf("Kind = %q", d.Kind)
	}
	if d.MinHostVersion != "0.4.0" {
		t.Fatalf("MinHostVersion = %q", d.MinHostVersion)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("valid manifest failed Validate: %v", err)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	if _, err := manifest.Parse([]byte(validProviderManifest + "\nbogus: z\n")); err == nil {
		t.Fatalf("strict parse must reject unknown field")
	}
}

func TestValidateRejectsUnknownKind(t *testing.T) {
	d := manifest.Doc{
		Name:       "x",
		Implements: connector.ClassCredentialLoader,
		Kind:       manifest.Kind("bogus"),
	}
	err := d.Validate()
	if err == nil {
		t.Fatalf("unknown kind must fail Validate")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Fatalf("error should mention kind: %v", err)
	}
}

func TestValidateRejectsUnknownImplements(t *testing.T) {
	d := manifest.Doc{
		Name:       "x",
		Implements: connector.Class("NotAThing"),
		Kind:       manifest.KindDeclarative,
	}
	err := d.Validate()
	if err == nil {
		t.Fatalf("unknown implements must fail Validate")
	}
	if !strings.Contains(err.Error(), "implements") {
		t.Fatalf("error should mention implements: %v", err)
	}
}

func TestValidateRejectsLiteralSecretInAuth(t *testing.T) {
	d := manifest.Doc{
		Name:       "x",
		Implements: connector.ClassCredentialLoader,
		Kind:       manifest.KindDeclarative,
		Auth: map[string]string{
			"api_key": "sk_live_51H8xJ2example4Material",
		},
	}
	err := d.Validate()
	if err == nil {
		t.Fatalf("literal secret material in Auth must fail Validate")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("error should name the offending auth entry: %v", err)
	}
}

func TestValidateAcceptsRefAndEnvNameAuth(t *testing.T) {
	d := manifest.Doc{
		Name:       "x",
		Implements: connector.ClassCredentialLoader,
		Kind:       manifest.KindDeclarative,
		Auth: map[string]string{
			"env_style": "PLACEHOLDER_TOKEN",
			"ref_style": "op://<vault>/<item>/<field>",
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("ref/env-name auth entries must validate: %v", err)
	}
}

func TestValidateRejectsInvalidRefFormat(t *testing.T) {
	d := manifest.Doc{
		Name:       "x",
		Implements: connector.ClassCredentialLoader,
		Kind:       manifest.KindDeclarative,
		Auth: map[string]string{
			"bad_ref": "op://only-one-segment",
		},
	}
	if err := d.Validate(); err == nil {
		t.Fatalf("malformed op:// ref must fail Validate")
	}
}

func TestValidateRequiresMinHostVersionForProvider(t *testing.T) {
	d := manifest.Doc{
		Name:       "x",
		Implements: connector.ClassCredentialLoader,
		Kind:       manifest.KindProvider,
	}
	err := d.Validate()
	if err == nil {
		t.Fatalf("kind: provider without min_host_version must fail Validate")
	}
	if !strings.Contains(err.Error(), "min_host_version") {
		t.Fatalf("error should mention min_host_version: %v", err)
	}
}

func TestValidateDoesNotRequireMinHostVersionForDeclarative(t *testing.T) {
	d := manifest.Doc{
		Name:       "x",
		Implements: connector.ClassCredentialLoader,
		Kind:       manifest.KindDeclarative,
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("declarative manifest without min_host_version should validate: %v", err)
	}
}

func TestValidateRequiresName(t *testing.T) {
	d := manifest.Doc{
		Implements: connector.ClassCredentialLoader,
		Kind:       manifest.KindDeclarative,
	}
	if err := d.Validate(); err == nil {
		t.Fatalf("empty name must fail Validate")
	}
}

// TestCapabilitiesAreDeclaredInTheManifest covers the declaration half of
// REQ-ARQ-P-20: batch resolution is something a connector author WRITES DOWN,
// because the manifest is what an external author encodes and what a host
// reads before it plans a call pattern.
func TestCapabilitiesAreDeclaredInTheManifest(t *testing.T) {
	d, err := manifest.Parse([]byte(`
name: placeholder-credential-loader
implements: CredentialLoader
kind: native
capabilities: [read, batch_resolve]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !d.Declares(credential.CapBatchResolve) {
		t.Fatalf("manifest declaring batch_resolve must report Declares(CapBatchResolve)")
	}
	if !d.Declares(credential.CapRead) {
		t.Fatalf("manifest declaring read must report Declares(CapRead)")
	}
	if d.Declares(credential.CapLease) {
		t.Fatalf("Declares must be false for a capability the manifest does not list")
	}
	// Typed, not stringly: the declaration is comparable to the capability
	// constants the contract publishes, with no conversion at the call site.
	if len(d.Capabilities) != 2 || d.Capabilities[0] != credential.CapRead {
		t.Fatalf("Capabilities = %v, want typed connector.Capability values", d.Capabilities)
	}
}

func TestDeclaresOnAManifestWithNoCapabilities(t *testing.T) {
	var d manifest.Doc
	if d.Declares(credential.CapBatchResolve) {
		t.Fatalf("an empty manifest declares nothing")
	}
}

// TestValidateRejectsCapabilitiesOutsideTheClassVocabulary is the gap the
// typed Capabilities field did NOT close. connector.Capability is a string
// type, so YAML unmarshals anything at all into it: a Doc declaring
// "batch-resolve" (hyphen), "reed", or arbitrary text validated CLEAN, and
// only a full credconform run — which needs a live connector plus both
// fixtures — ever objected.
//
// Validate is what a host runs before it loads anything. A capability it does
// not recognise is one it will not use, so a misspelling silently becomes an
// undeclared capability and the connector loses the behaviour its author
// believed they had declared.
func TestValidateRejectsCapabilitiesOutsideTheClassVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name string
		caps []connector.Capability
	}{
		{"a plausible misspelling", []connector.Capability{credential.CapRead, "batch-resolve"}},
		{"a typo", []connector.Capability{"reed"}},
		{"arbitrary text", []connector.Capability{credential.CapRead, "rm -rf /"}},
		{"the empty capability", []connector.Capability{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := manifest.Doc{
				Name:         "placeholder-credential-loader",
				Implements:   connector.ClassCredentialLoader,
				Kind:         manifest.KindNative,
				Capabilities: tc.caps,
			}
			err := d.Validate()
			if err == nil {
				t.Fatalf("a capability outside the vocabulary must fail Validate: %v", tc.caps)
			}
			if !strings.Contains(err.Error(), "capabilit") {
				t.Fatalf("the error must say what is wrong: %v", err)
			}
			// It must name the offending value, or an author with a long
			// capability list has to bisect it by hand.
			if !strings.Contains(err.Error(), string(tc.caps[len(tc.caps)-1])) {
				t.Fatalf("the error must name the offending capability: %v", err)
			}
		})
	}
}

// TestValidateAcceptsEveryPublishedCapability is the control: closing the
// vocabulary must not reject the vocabulary. Declaring the whole published
// set at once has to validate, or the check is just a different bug.
func TestValidateAcceptsEveryPublishedCapability(t *testing.T) {
	d := manifest.Doc{
		Name:         "placeholder-credential-loader",
		Implements:   connector.ClassCredentialLoader,
		Kind:         manifest.KindNative,
		Capabilities: credential.KnownCapabilities(),
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("the published capability vocabulary must validate: %v", err)
	}
	if len(d.Capabilities) == 0 {
		t.Fatalf("KnownCapabilities() is empty; this test would pass for the wrong reason")
	}
}

// TestParsedManifestWithABadCapabilityFailsValidate walks the path an actual
// connector author takes — a connector.yml on disk — rather than a hand-built
// Doc. Parse is strict about unknown FIELDS and says nothing about unknown
// VALUES, so the capability list is exactly where a bad string gets in.
func TestParsedManifestWithABadCapabilityFailsValidate(t *testing.T) {
	d, err := manifest.Parse([]byte(`
name: placeholder-credential-loader
implements: CredentialLoader
kind: native
capabilities: [read, batch-resolve]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := d.Validate(); err == nil {
		t.Fatalf("a manifest file declaring batch-resolve must fail Validate")
	}
}
