package arqtossdk_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

// docs/CONTRACT.md is the fourth place the class set is written down, and it is
// the only one a compiler never reads.
//
// classset_test.go already pins the three CODE declarations against each other —
// the Class constants, the classes slice, and the manifest implements enum — on
// the stated grounds that each derivation "is trivially correct on the day it is
// written and silently wrong six months later". Prose has the same failure mode
// and no derivation at all, so it drifts faster and nothing notices.
//
// It has already cost twice, and both times the reader was external:
//
//   - A task specification told a contributor that Roster had no out-of-process
//     wire protocol and instructed them to STOP and confirm the runtime shape
//     with the maintainer. The wire protocol had landed the day after that
//     sentence was written. The document did not merely misinform; it told
//     someone to wait for something the repository already contained.
//   - A second document stated the class set was "exactly two" when it was four.
//     That document opens by declaring itself authoritative, so a reader who
//     trusted it as instructed had no second source to correct it against.
//
// The asymmetry is what makes this worth gating rather than absorbing: a stale
// document costs an EXTERNAL reader, who has no way to know it is stale and no
// access to the source of truth that would tell them. Every claim checked below
// is mechanically checkable — the class set is a closed slice in one place, a
// wire binding is a file on disk, and a package is a directory.
//
// # What this does NOT cover
//
// Only claims made in THIS repository. The same claims are restated in
// external-facing specifications that live elsewhere and cannot be read from
// here. The durable fix for those is for them to LINK to this document rather
// than restate it — a second copy of a fact is a second thing to drift, and this
// gate can only keep the first copy honest.

const contractDoc = "docs/CONTRACT.md"

// nativeOnlyMarker is the phrase whose PRECEDING class names are the document's
// claim about which classes lack a Track-B wire binding.
const nativeOnlyMarker = "are native-only today"

// classTableRow matches a row of CONTRACT.md's class table:
//
//	| [`Class`](#anchor) | what it adapts | [`pkg`](../pkg/x.go) | [`harness`](../harness/) |
//
// The three captured groups are the class name, the contract package and the
// conformance harness package.
var classTableRow = regexp.MustCompile(
	"(?m)^\\|\\s*\\[`([A-Za-z]+)`\\]\\([^)]*\\)\\s*\\|[^|]*\\|\\s*\\[`([a-z]+)`\\]\\([^)]*\\)\\s*\\|\\s*\\[`([a-z]+)`\\]\\([^)]*\\)\\s*\\|")

var backtickedName = regexp.MustCompile("`([A-Za-z]+)`")

func readContractDoc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(contractDoc)
	if err != nil {
		t.Fatalf("read %s: %v", contractDoc, err)
	}
	return string(b)
}

// TestContractDocClassTableMatchesTheClassSet pins the prose class table against
// connector.Classes() in BOTH directions. A class the SDK publishes but the
// document omits is undiscoverable to a reader who trusts the document; a class
// the document names but the SDK does not publish is a manifest that will be
// refused before a host loads anything.
func TestContractDocClassTableMatchesTheClassSet(t *testing.T) {
	rows := classTableRow.FindAllStringSubmatch(readContractDoc(t), -1)
	if len(rows) == 0 {
		t.Fatalf("%s: found no class-table rows — either the table was removed or its shape "+
			"changed and this gate silently stopped checking anything. Fix the pattern, do not "+
			"delete the test.", contractDoc)
	}

	var documented []connector.Class
	for _, r := range rows {
		documented = append(documented, connector.Class(r[1]))
	}
	slices.Sort(documented)

	published := slices.Clone(connector.Classes())
	slices.Sort(published)

	if !slices.Equal(documented, published) {
		t.Errorf("%s documents classes %v; connector.Classes() publishes %v.\n"+
			"A class is added by publishing a contract AND a conformance harness — and by saying "+
			"so here, because this document is what an external reader is pointed at.",
			contractDoc, documented, published)
	}
}

// TestContractDocClassTablePackagesExist checks that every contract package and
// conformance harness the table names is a real directory. A row pointing at a
// package that does not exist reads as authoritative and sends a reader nowhere.
func TestContractDocClassTablePackagesExist(t *testing.T) {
	rows := classTableRow.FindAllStringSubmatch(readContractDoc(t), -1)
	if len(rows) == 0 {
		t.Fatalf("%s: found no class-table rows; see the sibling test", contractDoc)
	}

	for _, r := range rows {
		class, contractPkg, harnessPkg := r[1], r[2], r[3]
		for _, pkg := range []string{contractPkg, harnessPkg} {
			info, err := os.Stat(pkg)
			if err != nil || !info.IsDir() {
				t.Errorf("%s: class %s names package %q, which is not a directory in this repo",
					contractDoc, class, pkg)
			}
		}
	}
}

// TestContractDocNativeOnlyClaimMatchesTheProtoFiles is the check that would have
// caught the costliest drift: the document claiming a class has no Track-B wire
// protocol after one landed.
//
// A class has a wire binding exactly when it has a .proto. That is the load-
// bearing artefact — the generated stubs, the plugin map and the host stub all
// follow from it, and it is a file, so the claim is decidable rather than
// remembered.
func TestContractDocNativeOnlyClaimMatchesTheProtoFiles(t *testing.T) {
	doc := readContractDoc(t)

	idx := strings.Index(doc, nativeOnlyMarker)
	if idx < 0 {
		t.Fatalf("%s: the phrase %q is gone, so the native-only claim can no longer be located "+
			"and this gate is checking nothing. If the wording changed, update the marker; if the "+
			"claim was removed, remove this test deliberately rather than letting it pass vacuously.",
			contractDoc, nativeOnlyMarker)
	}

	// The claim is the class names immediately BEFORE the marker. Everything
	// after it is the contrasting half of the same sentence — it names the
	// classes that DO have a wire binding — so reading the whole paragraph
	// would collect both sides and the check would always agree with itself.
	lookback := doc[:idx]
	if len(lookback) > 200 {
		lookback = lookback[len(lookback)-200:]
	}

	var claimedNativeOnly []connector.Class
	for _, m := range backtickedName.FindAllStringSubmatch(lookback, -1) {
		if c := connector.Class(m[1]); c.Valid() {
			claimedNativeOnly = append(claimedNativeOnly, c)
		}
	}
	slices.Sort(claimedNativeOnly)
	claimedNativeOnly = slices.Compact(claimedNativeOnly)

	if len(claimedNativeOnly) == 0 {
		t.Fatalf("%s: found the marker %q but no class names before it. The sentence shape changed "+
			"and the gate stopped checking; fix the parse rather than the assertion.",
			contractDoc, nativeOnlyMarker)
	}

	var actualNativeOnly []connector.Class
	for _, c := range connector.Classes() {
		proto := filepath.Join("proto", "connector", "v1", strings.ToLower(string(c))+".proto")
		if _, err := os.Stat(proto); os.IsNotExist(err) {
			actualNativeOnly = append(actualNativeOnly, c)
		} else if err != nil {
			t.Fatalf("stat %s: %v", proto, err)
		}
	}
	slices.Sort(actualNativeOnly)

	if !slices.Equal(claimedNativeOnly, actualNativeOnly) {
		t.Errorf("%s claims %v are native-only; the .proto files say %v are.\n"+
			"A class that GAINED a wire protocol while this sentence stood still is the exact "+
			"drift that told an external contributor to stop and wait for a capability this "+
			"repository already shipped.",
			contractDoc, claimedNativeOnly, actualNativeOnly)
	}
}
