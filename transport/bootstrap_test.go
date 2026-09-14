package transport_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/credential"
	"github.com/arqtiqa/arqtos-sdk-go/transport"
)

func TestBootstrapRoundTripOneKeyAndTwoKey(t *testing.T) {
	one := credential.NewBootstrap(map[string][]byte{"token": []byte("tok")})
	got := transport.BootstrapFromPB(transport.BootstrapToPB(one))
	if m := got.Get("token"); m == nil || string(m.Reveal()) != "tok" {
		t.Fatalf("one-key round-trip lost token")
	}
	two := credential.NewBootstrap(map[string][]byte{
		"client_id":     []byte("id"),
		"client_secret": []byte("sec"),
	})
	got = transport.BootstrapFromPB(transport.BootstrapToPB(two))
	id, sec := got.Get("client_id"), got.Get("client_secret")
	if id == nil || string(id.Reveal()) != "id" || sec == nil || string(sec.Reveal()) != "sec" {
		t.Fatalf("two-key round-trip lost a required key")
	}
}

func TestBootstrapToPBDoesNotChangeRedaction(t *testing.T) {
	boot := credential.NewBootstrap(map[string][]byte{"token": []byte("live-material")})
	_ = transport.BootstrapToPB(boot)
	if s := fmt.Sprintf("%v %#v", boot, boot); strings.Contains(s, "live-material") {
		t.Fatalf("marshalling leaked bootstrap: %q", s)
	}
}
