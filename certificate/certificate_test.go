package certificate_test

import (
	"slices"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/certificate"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

func TestClassCertificateIsPublished(t *testing.T) {
	if !connector.ClassCertificate.Valid() || !slices.Contains(connector.Classes(), connector.ClassCertificate) {
		t.Fatalf("ClassCertificate is not in connector.Classes() (%v)", connector.Classes())
	}
}

func TestKnownCapabilitiesIsEmptyAtV1(t *testing.T) {
	if got := certificate.KnownCapabilities(); len(got) != 0 {
		t.Fatalf("KnownCapabilities() = %v, want empty at v1", got)
	}
}
