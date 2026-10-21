package codehost

import (
	"errors"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

// RefuseInitialize is the typed refusal of [CreateRepoOpts.Initialize] by a
// connector that cannot create a repository with an initial revision. It is
// cerr.KindUnsupported, returned before any repository is created.
func RefuseInitialize(op string) error {
	return cerr.New(cerr.KindUnsupported, op,
		errors.New("provider cannot create a repository with an initial revision"))
}
