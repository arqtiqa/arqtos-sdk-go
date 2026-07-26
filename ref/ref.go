// Package ref is the op:// secret-reference used across connector contracts.
// A Ref is a *reference* to a secret, never the material itself (refs-only invariant).
package ref

import (
	"fmt"
	"strings"
)

type Ref struct {
	Vault string
	Item  string
	Field string
}

// Parse parses "op://<vault>/<item>/<field>". All three segments are required and non-empty.
func Parse(s string) (Ref, error) {
	const scheme = "op://"
	if !strings.HasPrefix(s, scheme) {
		return Ref{}, fmt.Errorf("ref: missing %q scheme in %q", scheme, s)
	}
	parts := strings.Split(strings.TrimPrefix(s, scheme), "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Ref{}, fmt.Errorf("ref: want op://<vault>/<item>/<field>, got %q", s)
	}
	return Ref{Vault: parts[0], Item: parts[1], Field: parts[2]}, nil
}

func (r Ref) String() string { return "op://" + r.Vault + "/" + r.Item + "/" + r.Field }
