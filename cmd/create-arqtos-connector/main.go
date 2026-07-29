// Command create-arqtos-connector scaffolds a new out-of-process (Track-B)
// arqtos Roster connector project — the shape a third-party connector
// author actually ships, and the one rosterconform.RunOutOfProcess is the
// gate for.
//
// Usage:
//
//	create-arqtos-connector -name okta-roster -module github.com/you/okta-roster-connector [-out ./okta-roster-connector]
//
// The generated project compiles and passes its own conformance test right
// now, against a fixed placeholder directory — before a single line of real
// logic exists. See the generated main.go for what to change and, more
// importantly, the two mistakes this contract has already made someone pay
// for once each.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/arqtiqa/arqtos-sdk-go/scaffold"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, tidy))
}

// tidyFunc runs `go mod tidy` in dir and returns its combined output. It is
// a parameter of run rather than a hardcoded call so tests can exercise
// run's success/failure reporting without a real network round trip.
type tidyFunc func(dir string) ([]byte, error)

// tidy is the real implementation: `go mod tidy`, run inside the freshly
// generated project. This is a convenience step for the CLI, not something
// Generate does itself — Generate performs no network access, so it stays
// fast and offline-testable; only this command-line wrapper touches the
// network, and only after the files are already safely on disk.
func tidy(dir string) ([]byte, error) {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func run(args []string, stdout, stderr io.Writer, tidyFn tidyFunc) int {
	fs := flag.NewFlagSet("create-arqtos-connector", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "connector name, e.g. okta-roster (required; becomes connector.yml's name)")
	module := fs.String("module", "", "Go module path for the generated project, e.g. github.com/you/okta-roster-connector (required)")
	out := fs.String("out", "", "output directory (default: ./<name>-connector)")
	if err := fs.Parse(args); err != nil {
		return 2 // flag already printed its own error/usage to stderr
	}

	opts := scaffold.Options{Name: *name, Module: *module}
	if err := opts.Validate(); err != nil {
		fmt.Fprintf(stderr, "create-arqtos-connector: %v\n", err)
		fs.Usage()
		return 2
	}

	dir := *out
	if dir == "" {
		dir = "./" + *name + "-connector"
	}

	if err := scaffold.Generate(dir, opts); err != nil {
		fmt.Fprintf(stderr, "create-arqtos-connector: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote a Roster connector skeleton to %s\n", dir)

	if out, err := tidyFn(dir); err != nil {
		fmt.Fprintf(stderr,
			"create-arqtos-connector: go mod tidy failed in %s (%v); run it yourself before building:\n%s\n",
			dir, err, out)
	} else {
		fmt.Fprintf(stdout, "go mod tidy: ok\n")
	}

	fmt.Fprintf(stdout, "\nnext:\n  cd %s\n  go build ./...\n  go test ./...\n", dir)
	return 0
}
