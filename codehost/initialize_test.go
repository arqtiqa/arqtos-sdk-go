package codehost

import (
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

func TestCreateRepoOpts_Initialize_DefaultsToEmptyRepository(t *testing.T) {
	if (CreateRepoOpts{Owner: "o", Name: "n"}).Initialize {
		t.Fatal("Initialize must be opt-in: the zero value creates the repository as before")
	}
}

func TestRefuseInitialize_IsTypedUnsupportedNamingTheOperation(t *testing.T) {
	err := RefuseInitialize("CreateRepo")
	if err == nil {
		t.Fatal("RefuseInitialize returned nil")
	}
	if got := cerr.KindOf(err); got != cerr.KindUnsupported {
		t.Fatalf("kind=%v; want unsupported", got)
	}
	if !strings.Contains(err.Error(), "initial revision") {
		t.Fatalf("error %q does not name the unmet option", err)
	}
}
