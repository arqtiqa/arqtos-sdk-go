package skillspec_test

import (
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/skillspec"
)

func TestParseValid(t *testing.T) {
	s, err := skillspec.Parse([]byte("name: berg-authoring\ndescription: author berg docs\n"))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("valid skill failed Validate: %v", err)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	if _, err := skillspec.Parse([]byte("name: x\ndescription: y\nbogus: z\n")); err == nil {
		t.Fatalf("strict parse must reject unknown field")
	}
}

func TestValidateRequiresNameAndDescription(t *testing.T) {
	s := skillspec.Skill{Name: ""}
	if err := s.Validate(); err == nil {
		t.Fatalf("empty name must fail Validate")
	}
}
