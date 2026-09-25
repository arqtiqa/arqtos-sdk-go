// Command tagmod creates the annotated module tag after CheckTagFreeze.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/compat"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, "usage: tagmod <reviewed-sha>\n")
		return 2
	}
	freeze := args[0]
	if err := git("fetch", "origin", "--tags"); err != nil {
		fmt.Fprintf(stderr, "tagmod: %v\n", err)
		return 1
	}
	originMain, err := gitOut("rev-parse", "origin/main")
	if err != nil {
		fmt.Fprintf(stderr, "tagmod: %v\n", err)
		return 1
	}
	existing, _ := gitOut("rev-parse", compat.ModuleVersion+"^{commit}")
	if err := compat.CheckTagFreeze(compat.ModuleVersion, freeze, originMain, existing); err != nil {
		fmt.Fprintf(stderr, "tagmod: %v\n", err)
		return 1
	}
	if existing == freeze {
		fmt.Fprintf(stdout, "tagmod: %s already at %s\n", compat.ModuleVersion, freeze)
		return 0
	}
	msg := "arqtos-sdk-go " + strings.TrimPrefix(compat.ModuleVersion, "v") + ": Line-5 module pin"
	if err := git("tag", "-a", compat.ModuleVersion, freeze, "-m", msg); err != nil {
		fmt.Fprintf(stderr, "tagmod: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "tagmod: created %s at %s\n", compat.ModuleVersion, freeze)
	return 0
}

func git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gitOut(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
