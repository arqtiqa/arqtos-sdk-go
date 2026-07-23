package cerr_test

import (
	"errors"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

func TestKindOfAndRetryable(t *testing.T) {
	base := errors.New("boom")
	e := cerr.New(cerr.KindUnavailable, "Resolve", base)

	if cerr.KindOf(e) != cerr.KindUnavailable {
		t.Fatalf("KindOf = %v", cerr.KindOf(e))
	}
	if !errors.Is(e, base) {
		t.Fatalf("Unwrap chain broken")
	}
	if !cerr.Retryable(e) {
		t.Fatalf("Unavailable must be retryable")
	}
	if cerr.Retryable(cerr.New(cerr.KindNotFound, "Resolve", nil)) {
		t.Fatalf("NotFound must not be retryable")
	}
	if cerr.KindOf(base) != cerr.KindUnknown {
		t.Fatalf("plain error must classify as Unknown")
	}
}
