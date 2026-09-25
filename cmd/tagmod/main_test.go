package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestRun_MissingSHA(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	code := run(nil, os.Stdout, w)
	_ = w.Close()
	got, _ := io.ReadAll(r)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(string(got), "usage:") {
		t.Fatalf("stderr = %q", got)
	}
}
