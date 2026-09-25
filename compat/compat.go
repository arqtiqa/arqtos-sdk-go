// Package compat declares the SDK contracts the first line-5 runtime consumes.
// A pin is an exact module version, never an untracked branch.
//
// arqtos-sdk-go#193.
package compat

import (
	"errors"
	"fmt"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

var errEmptySHA = errors.New("compat: freeze SHA and origin/main SHA are required")

// ModulePath is the public module path a consumer requires.
const ModulePath = "github.com/arqtiqa/arqtos-sdk-go"

// ModuleVersion is the stable module version Core and connectors pin
// (arqtos-sdk-go#204). Not an alpha or pseudoversion.
const ModuleVersion = "v0.5.0"

// CheckTagFreeze refuses a moved origin/main SHA or a tag already pointing
// at a different commit. existingTagSHA is empty when the tag is absent.
func CheckTagFreeze(tag, freezeSHA, originMainSHA, existingTagSHA string) error {
	if freezeSHA == "" || originMainSHA == "" {
		return errEmptySHA
	}
	if freezeSHA != originMainSHA {
		return fmt.Errorf("compat: source moved: freeze %s origin/main %s", freezeSHA, originMainSHA)
	}
	if existingTagSHA != "" && existingTagSHA != freezeSHA {
		return fmt.Errorf("compat: tag %s already points at %s, not %s", tag, existingTagSHA, freezeSHA)
	}
	return nil
}

// Line5Runtime is the connector-class set the first line-5 runtime consumes.
func Line5Runtime() []connector.Class {
	return []connector.Class{
		connector.ClassCredentialLoader,
		connector.ClassRoster,
		connector.ClassCodeCI,
		connector.ClassTracker,
		connector.ClassAuthenticator,
		connector.ClassCodeHost,
		connector.ClassSearch,
		connector.ClassCertificate,
	}
}
