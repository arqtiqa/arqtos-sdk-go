package plugin_test

import (
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/manifest"
	"github.com/arqtiqa/arqtos-sdk-go/plugin"
)

func TestCertificatePluginMapAndHostMap(t *testing.T) {
	if _, ok := plugin.CertificatePluginMap(nil)[plugin.CertificateName]; !ok {
		t.Fatalf("CertificatePluginMap missing key %q", plugin.CertificateName)
	}
	doc := manifest.Doc{Name: "vault-transit", Implements: connector.ClassCertificate, Kind: manifest.KindProvider, MinHostVersion: "0.5.0"}
	m := plugin.CertificateHostPluginMap("vault-transit", doc, "0.5.0")
	p, ok := m[plugin.CertificateName].(*plugin.CertificatePlugin)
	if !ok {
		t.Fatalf("plugin under key %q is %T", plugin.CertificateName, m[plugin.CertificateName])
	}
	if p.Name != "vault-transit" || p.HostVersion != "0.5.0" || p.ProviderManifest.Name != "vault-transit" {
		t.Fatalf("negotiation inputs not carried: %+v", p)
	}
}
