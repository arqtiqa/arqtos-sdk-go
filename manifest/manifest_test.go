package manifest_test

import (
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
)

const validProviderManifest = `
name: infisical-credential-loader
implements: CredentialLoader
kind: provider
schema_version: "1"
capabilities: [read, lease]
min_host_version: "0.4.0"
supports:
  rotate: "false"
auth:
  api_token: INFISICAL_TOKEN
  service_ref: op://infra/infisical/token
`

func TestParseValidProviderManifest(t *testing.T) {
	d, err := manifest.Parse([]byte(validProviderManifest))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if d.Name != "infisical-credential-loader" {
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
			"env_style": "INFISICAL_TOKEN",
			"ref_style": "op://infra/infisical/token",
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
