package plugin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
	"github.com/arqtiqa/arqtos-sdk-go/credential"
)

type authMemLoader struct {
	memLoader
	got *credential.Bootstrap
}

func (a *authMemLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapBindAuth}
}

func (a *authMemLoader) BindAuth(_ context.Context, boot *credential.Bootstrap) error {
	a.got = boot
	return nil
}

var _ credential.AuthBinder = (*authMemLoader)(nil)

type declaresAuthButCannot struct{ memLoader }

func (d *declaresAuthButCannot) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapBindAuth}
}

func TestStubShapeMirrorsBindAuthDeclaration(t *testing.T) {
	t.Run("declared: the stub binds", func(t *testing.T) {
		c := newTestClient(t, &authMemLoader{memLoader: memLoader{vals: batchVals()}})
		if _, ok := c.(credential.AuthBinder); !ok {
			t.Fatalf("a provider reporting %s must dispense a credential.AuthBinder, got %T",
				credential.CapBindAuth, c)
		}
	})
	t.Run("not declared: the stub does not", func(t *testing.T) {
		c := newTestClient(t, &memLoader{vals: batchVals()})
		if _, ok := c.(credential.AuthBinder); ok {
			t.Fatalf("a provider that does not report %s must not dispense a credential.AuthBinder",
				credential.CapBindAuth)
		}
	})
}

func TestBindAuthRoundTripOneKeyAndTwoKeyOverTheWire(t *testing.T) {
	impl := &authMemLoader{memLoader: memLoader{vals: batchVals()}}
	c := newTestClient(t, impl)
	b, ok := c.(credential.AuthBinder)
	if !ok {
		t.Fatalf("dispensed %T, want a credential.AuthBinder", c)
	}

	one := credential.NewBootstrap(map[string][]byte{"token": []byte("tok")})
	if err := b.BindAuth(context.Background(), one); err != nil {
		t.Fatalf("one-key BindAuth: %v", err)
	}
	if impl.got == nil || string(impl.got.Get("token").Reveal()) != "tok" {
		t.Fatalf("one-key material did not arrive on the provider")
	}

	two := credential.NewBootstrap(map[string][]byte{
		"client_id":     []byte("id"),
		"client_secret": []byte("sec"),
	})
	if err := b.BindAuth(context.Background(), two); err != nil {
		t.Fatalf("two-key BindAuth: %v", err)
	}
	if string(impl.got.Get("client_id").Reveal()) != "id" || string(impl.got.Get("client_secret").Reveal()) != "sec" {
		t.Fatalf("two-key material did not arrive on the provider")
	}
	if s := fmt.Sprintf("%v", impl.got); strings.Contains(s, "sec") {
		t.Fatalf("provider-held bootstrap leaked: %q", s)
	}
}

func TestBindAuthDoesNotSubstituteAmbientCredentials(t *testing.T) {
	const ambient = "AMBIENT_BOOTSTRAP_VALUE"
	t.Setenv("TOKEN", ambient)
	impl := &authMemLoader{memLoader: memLoader{vals: batchVals()}}
	c := newTestClient(t, impl)
	b, ok := c.(credential.AuthBinder)
	if !ok {
		t.Fatalf("dispensed %T, want a credential.AuthBinder", c)
	}
	empty := credential.NewBootstrap(map[string][]byte{})
	if err := b.BindAuth(context.Background(), empty); err != nil {
		t.Fatalf("BindAuth: %v", err)
	}
	if impl.got.Get("token") != nil {
		t.Fatal("BindAuth substituted an inherited environment value")
	}
	if v := os.Getenv("TOKEN"); v != ambient {
		t.Fatalf("test env was disturbed: %q", v)
	}
}

func TestBindAuthDeclaredButNotImplementedAnswersUnsupported(t *testing.T) {
	c := newTestClient(t, &declaresAuthButCannot{memLoader{vals: batchVals()}})
	b, ok := c.(credential.AuthBinder)
	if !ok {
		t.Fatalf("dispensed %T, want a credential.AuthBinder for a provider that declares bind_auth", c)
	}
	err := b.BindAuth(context.Background(), credential.NewBootstrap(map[string][]byte{"token": []byte("tok")}))
	if err == nil {
		t.Fatal("a provider that declares bind_auth without implementing it must fail the call")
	}
	if cerr.KindOf(err) != cerr.KindUnsupported {
		t.Fatalf("KindOf = %v, want KindUnsupported", cerr.KindOf(err))
	}
}

func TestOldPeerWithoutBindAuthIsUnsupportedNotSilent(t *testing.T) {
	c := newTestClient(t, &memLoader{vals: batchVals()})
	if _, ok := c.(credential.AuthBinder); ok {
		t.Fatal("an old peer must not grow BindAuth by default")
	}
}

type batchAuthMemLoader struct {
	batchMemLoader
	got *credential.Bootstrap
}

func (b *batchAuthMemLoader) Capabilities() connector.Capabilities {
	return connector.Capabilities{credential.CapRead, credential.CapBatchResolve, credential.CapBindAuth}
}

func (b *batchAuthMemLoader) BindAuth(_ context.Context, boot *credential.Bootstrap) error {
	b.got = boot
	return nil
}

func TestStubShapeMirrorsBatchAndBindAuthTogether(t *testing.T) {
	c := newTestClient(t, &batchAuthMemLoader{batchMemLoader: batchMemLoader{memLoader{vals: batchVals()}}})
	if _, ok := c.(credential.BatchResolver); !ok {
		t.Fatalf("dispensed %T, want BatchResolver", c)
	}
	if _, ok := c.(credential.AuthBinder); !ok {
		t.Fatalf("dispensed %T, want AuthBinder", c)
	}
}
