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

// The class table is read by TWO checks that ask different questions, so it is
// matched by two patterns rather than one.
//
// Conflating them was a real defect, found the first time a class was added
// after this file shipped. A single full-row pattern made "this class is
// documented" and "this class has both a contract package and a harness on
// disk" the SAME assertion — so a class could not be published until its
// harness existed, while the harness could not compile until the class was
// published. The circularity was created here, not by the SDK.
//
// classTableClassCell matches the FIRST cell only: a backticked class name
// linked to an in-document anchor. It answers "is this class documented at
// all", and it matches a row whose later columns are still prose.
var classTableClassCell = regexp.MustCompile("(?m)^\\|\\s*\\[`([A-Za-z]+)`\\]\\(#[^)]*\\)\\s*\\|.*$")

// packageLink matches a backticked package name linked to a relative path,
// wherever one appears in a class-table row. It answers the narrower question:
// of the packages a row actually NAMES, does each exist? A row that names none
// is not checked, because a column reading "lands with the harness" points a
// reader at the truth rather than at a missing directory.
var packageLink = regexp.MustCompile("\\[`([a-z]+)`\\]\\((\\.\\./[^)]*)\\)")

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
	rows := classTableClassCell.FindAllStringSubmatch(readContractDoc(t), -1)
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
	rows := classTableClassCell.FindAllStringSubmatch(readContractDoc(t), -1)
	if len(rows) == 0 {
		t.Fatalf("%s: found no class-table rows; see the sibling test", contractDoc)
	}

	var checked int
	for _, r := range rows {
		class, line := r[1], r[0]
		for _, link := range packageLink.FindAllStringSubmatch(line, -1) {
			pkg := link[1]
			checked++
			info, err := os.Stat(pkg)
			if err != nil || !info.IsDir() {
				t.Errorf("%s: class %s names package %q, which is not a directory in this repo",
					contractDoc, class, pkg)
			}
		}
	}

	// A class whose row names no package at all is legitimate while its
	// contract or harness is still landing. A table where NOTHING names a
	// package is not — it means the link shape changed and this check went
	// quiet.
	if checked == 0 {
		t.Fatalf("%s: no class row names a package at all, so this check verified nothing. "+
			"The link shape probably changed; fix the pattern rather than deleting the test.",
			contractDoc)
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

	// The claim is the class names before the marker, IN ITS OWN PARAGRAPH.
	//
	// Everything after the marker is the contrasting half of the same sentence
	// — it names the classes that DO have a wire binding — so reading forward
	// would collect both sides and the check would always agree with itself.
	//
	// ⚠️ The contrast can also sit BEFORE, and a fixed-size lookback does not
	// survive that. This was measured: when Authenticator gained a wire
	// protocol, the sentence naming the three classes that HAVE one landed in
	// the preceding paragraph, drifted inside a 200-character window, and made
	// the check report a class as claimed-native-only that the document says
	// the opposite about. It then passed again only because an edit pushed that
	// sentence ~30 characters further away — a green that depended on
	// line-wrapping.
	//
	// The paragraph boundary is the real unit of the claim, so scope to it.
	lookback := doc[:idx]
	if para := strings.LastIndex(lookback, "\n\n"); para >= 0 {
		lookback = lookback[para+2:]
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
