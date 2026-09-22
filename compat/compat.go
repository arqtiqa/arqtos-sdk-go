// Package compat declares the SDK contracts the first line-5 runtime consumes.
// A pin is an exact module version, never an untracked branch.
//
// arqtos-sdk-go#193.
package compat

import "github.com/arqtiqa/arqtos-sdk-go/connector"

// ModulePath is the public module path a consumer requires.
const ModulePath = "github.com/arqtiqa/arqtos-sdk-go"

// Line5Runtime is the connector-class set the first line-5 runtime consumes.
func Line5Runtime() []connector.Class {
	return nil
}
