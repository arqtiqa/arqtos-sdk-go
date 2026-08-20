package tapeformat_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/tapeformat"
)

func acceptedAt(min int) contracts.AcceptedTime {
	return contracts.AcceptedTime{
		At:        time.Date(2026, 8, 20, 0, min, 0, 0, time.UTC),
		Authority: contracts.TimeAuthority{Name: "authority:acceptor", Provenance: contracts.ClockSynchronised},
	}
}

func actID(n int) string { return "sha256:" + strings.Repeat(strconv.Itoa(n%10), 64) }

func entry(seq int, parent string) tapeformat.Entry {
	return tapeformat.Entry{
		SchemaVersion:   tapeformat.SchemaVersion,
		Sequence:        itoa(seq),
		ActBodyID:       actID(seq),
		ParentActBodyID: parent,
		AcceptedAt:      acceptedAt(seq),
	}
}

// ⚠️ strconv, not a rune arithmetic shortcut: the shortcut silently produces
// nonsense past 9, and the failure would look like a sequencing defect in the
// code under test rather than in the fixture.
func itoa(n int) string { return strconv.Itoa(n) }

func chain(n int) []tapeformat.Entry {
	out := make([]tapeformat.Entry, 0, n)
	parent := ""
	for i := range n {
		e := entry(i, parent)
		out = append(out, e)
		parent = e.ActBodyID
	}
	return out
}

// ---------------------------------------------------------------------------
// The entry
// ---------------------------------------------------------------------------

func TestEntry_AcceptsAWellFormedEntry(t *testing.T) {
	if err := entry(1, actID(0)).Validate(); err != nil {
		t.Fatalf("Validate rejected a well-formed entry: %v", err)
	}
}

func TestEntry_RefusesAnyMissingField(t *testing.T) {
	cases := map[string]func(*tapeformat.Entry){
		"schema_version": func(e *tapeformat.Entry) { e.SchemaVersion = "" },
		"sequence":       func(e *tapeformat.Entry) { e.Sequence = "" },
		"act_body_id":    func(e *tapeformat.Entry) { e.ActBodyID = "" },
		"accepted_at":    func(e *tapeformat.Entry) { e.AcceptedAt = contracts.AcceptedTime{} },
	}
	for name, blank := range cases {
		t.Run(name, func(t *testing.T) {
			e := entry(1, actID(0))
			blank(&e)
			err := e.Validate()
			if err == nil {
				t.Fatalf("Validate accepted an entry with no %s", name)
			}
			if !errors.Is(err, tapeformat.ErrInvalidEntry) {
				t.Errorf("error %v does not wrap ErrInvalidEntry", err)
			}
		})
	}
	// ⚠️ ParentActBodyID is legitimately empty at sequence 0, so it is NOT in
	// this table — and the count is asserted against the struct so a field added
	// without a case is a failure rather than a silent gap.
	if want := reflect.TypeOf(tapeformat.Entry{}).NumField() - 1; len(cases) != want {
		t.Errorf("checked %d fields, the entry has %d requiring a case", len(cases), want)
	}
}

// ⚠️ Sequence is a STRING because this record is canonically encoded and the
// canonical form forbids JSON numbers — but it must still BE a number, or the
// ordering the tape depends on is a string sort.
func TestEntry_RefusesASequenceThatIsNotANumber(t *testing.T) {
	for name, seq := range map[string]string{
		"words":    "first",
		"negative": "-1",
		"padded":   "007",
		"float":    "1.0",
	} {
		t.Run(name, func(t *testing.T) {
			e := entry(1, actID(0))
			e.Sequence = seq
			if err := e.Validate(); !errors.Is(err, tapeformat.ErrInvalidEntry) {
				t.Fatalf("Validate accepted sequence %q: %v", seq, err)
			}
		})
	}
}

// ⚠️ Canonically encodable, or an entry cannot be digested — and the failure
// would not appear until the first time an identity was computed.
func TestEntry_IsCanonicallyEncodable(t *testing.T) {
	if _, err := entry(1, actID(0)).Digest(); err != nil {
		t.Fatalf("an entry cannot be canonically encoded: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The chain
// ---------------------------------------------------------------------------

func TestVerifyChain_AcceptsALinearChain(t *testing.T) {
	if err := tapeformat.VerifyChain(chain(4)); err != nil {
		t.Fatalf("a linear chain was refused: %v", err)
	}
}

func TestVerifyChain_RefusesAnEmptyTape(t *testing.T) {
	if err := tapeformat.VerifyChain(nil); err == nil {
		t.Fatal("an empty tape was accepted — a chain with no genesis entry is not a chain")
	}
}

// ⚠️ The FIRST entry is the one nothing precedes. An entry at sequence 0 that
// names a parent is a tape whose start is somewhere this tape cannot show.
func TestVerifyChain_RefusesAFirstEntryThatNamesAParent(t *testing.T) {
	c := chain(2)
	c[0].ParentActBodyID = actID(9)
	if err := tapeformat.VerifyChain(c); !errors.Is(err, tapeformat.ErrBrokenChain) {
		t.Fatalf("a first entry naming a parent was accepted: %v", err)
	}
}

func TestVerifyChain_RefusesALaterEntryWithNoParent(t *testing.T) {
	c := chain(2)
	c[1].ParentActBodyID = ""
	err := tapeformat.VerifyChain(c)
	if !errors.Is(err, tapeformat.ErrBrokenChain) {
		t.Fatalf("a later entry with no parent was accepted: %v", err)
	}
	// ⚠️ The REASON again: the parent-LINK comparison next to this rule catches
	// an empty parent too, and wraps the same sentinel. Asserting only the
	// sentinel left this rule untested.
	if !strings.Contains(err.Error(), "names no parent") {
		t.Errorf("error %q does not name the missing parent; the link comparison may be firing instead", err)
	}
}

func TestVerifyChain_RefusesABrokenParentLink(t *testing.T) {
	c := chain(3)
	c[2].ParentActBodyID = actID(0) // its predecessor is entry 1, not 0
	err := tapeformat.VerifyChain(c)
	if !errors.Is(err, tapeformat.ErrBrokenChain) {
		t.Fatalf("an entry linking past its predecessor was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("error %q does not name the offending entry", err)
	}
}

// ⚠️ A gap is a MISSING ENTRY, and reading past one silently would turn a
// truncated middle into a shorter valid tape.
func TestVerifyChain_RefusesAGapInTheSequence(t *testing.T) {
	c := chain(3)
	c[2].Sequence = "5"
	if err := tapeformat.VerifyChain(c); !errors.Is(err, tapeformat.ErrBrokenChain) {
		t.Fatalf("a gap in the sequence was accepted: %v", err)
	}
}

// ⚠️ One act, one position. An act appearing twice would let a spend be replayed
// by re-listing it, which is the cheapest possible double-spend.
func TestVerifyChain_RefusesARepeatedAct(t *testing.T) {
	c := chain(3)
	c[2].ActBodyID = c[1].ActBodyID
	c[2].ParentActBodyID = c[1].ActBodyID
	if err := tapeformat.VerifyChain(c); !errors.Is(err, tapeformat.ErrBrokenChain) {
		t.Fatalf("the same act at two positions was accepted: %v", err)
	}
}

func TestVerifyChain_RefusesAnUnusableEntry(t *testing.T) {
	c := chain(2)
	c[1].AcceptedAt = contracts.AcceptedTime{}
	if err := tapeformat.VerifyChain(c); !errors.Is(err, tapeformat.ErrInvalidEntry) {
		t.Fatalf("a chain carrying an unusable entry was accepted: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The stream — where truncation is detected
// ---------------------------------------------------------------------------

func golden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading the golden tape: %v", err)
	}
	return raw
}

func TestReadStream_ReadsAWellFormedTape(t *testing.T) {
	entries, err := tapeformat.ReadStream(strings.NewReader(string(golden(t, "well-formed.tape"))))
	if err != nil {
		t.Fatalf("a well-formed tape was refused: %v", err)
	}
	// ⚠️ The COUNT is asserted, not just the absence of an error. A reader that
	// returned nothing would otherwise pass this.
	if len(entries) != 3 {
		t.Fatalf("read %d entries, want 3", len(entries))
	}
	for i, e := range entries {
		if e.Sequence != itoa(i) {
			t.Errorf("entry %d is at sequence %q", i, e.Sequence)
		}
	}
}

// ⚠️ THE FAILURE THAT MOST READS LIKE SUCCESS. A tape cut short at an entry
// boundary is indistinguishable from a shorter complete tape unless the count is
// DECLARED — which is why the header declares it and the reader checks it.
func TestReadStream_RefusesATruncatedTape(t *testing.T) {
	_, err := tapeformat.ReadStream(strings.NewReader(string(golden(t, "truncated.tape"))))
	if err == nil {
		t.Fatal("a truncated tape was read as a complete shorter one")
	}
	if !errors.Is(err, tapeformat.ErrTruncated) {
		t.Errorf("error %v does not wrap ErrTruncated", err)
	}
	if !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "2") {
		t.Errorf("error %q does not name both the declared and the actual count", err)
	}
}

func TestReadStream_RefusesTrailingEntries(t *testing.T) {
	raw := string(golden(t, "well-formed.tape"))
	extra := raw + `{"schema_version":"1","sequence":"3","act_body_id":"sha256:` + strings.Repeat("9", 64) + `","parent_act_body_id":"sha256:` + strings.Repeat("2", 64) + `","accepted_at":{"at":"2026-08-20T00:03:00Z","authority":{"name":"authority:acceptor","provenance":"synchronised"}}}` + "\n"
	_, err := tapeformat.ReadStream(strings.NewReader(extra))
	if err == nil {
		t.Fatal("a tape carrying more entries than it declares was accepted")
	}
	// ⚠️ The REASON is asserted. Without the explicit check, the count assertion
	// catches this too — and reports it as TRUNCATION, which is the opposite of
	// the truth. A test asserting only "some error" passed on that.
	if errors.Is(err, tapeformat.ErrTruncated) {
		t.Errorf("a tape with EXTRA entries was reported as truncated: %v", err)
	}
	if !strings.Contains(err.Error(), "more entries") {
		t.Errorf("error %q does not say the stream carries more than it declares", err)
	}
}

func TestReadStream_RefusesABrokenChain(t *testing.T) {
	if _, err := tapeformat.ReadStream(strings.NewReader(string(golden(t, "broken-link.tape")))); !errors.Is(err, tapeformat.ErrBrokenChain) {
		t.Fatalf("a tape whose links do not join was accepted: %v", err)
	}
}

// ⚠️ A tape is HOSTILE INPUT. A field the reader skips is a field the writer may
// have meant, so the two agree on bytes while disagreeing on meaning.
func TestReadStream_RefusesAnUnknownField(t *testing.T) {
	raw := strings.Replace(string(golden(t, "well-formed.tape")), `{"schema_version"`, `{"an_extra_field":"x","schema_version"`, 1)
	if _, err := tapeformat.ReadStream(strings.NewReader(raw)); err == nil {
		t.Fatal("an entry carrying a field the reader does not know was accepted")
	}
}

func TestReadStream_RefusesAMissingOrMalformedHeader(t *testing.T) {
	// ⚠️ Each case names the phrase that identifies ITS cause. Asserting only
	// "some error" let three of these pass on an unrelated refusal downstream —
	// a malformed header with a zero count reaches the empty-tape check, which
	// fails for a reason that has nothing to do with the header.
	valid := string(golden(t, "well-formed.tape"))
	entries := strings.SplitN(valid, "\n", 2)[1]

	for name, c := range map[string]struct{ raw, want string }{
		"empty":         {"", "empty"},
		"no header":     {`{"schema_version":"1","sequence":"0"}` + "\n", "header"},
		"wrong format":  {`{"schema_version":"1","format":"something.else","entry_count":"3"}` + "\n" + entries, "format"},
		"count missing": {`{"schema_version":"1","format":"arqtos.tape.v1"}` + "\n" + entries, "entry_count"},
		"count words":   {`{"schema_version":"1","format":"arqtos.tape.v1","entry_count":"three"}` + "\n" + entries, "entry_count"},
		"count padded":  {`{"schema_version":"1","format":"arqtos.tape.v1","entry_count":"003"}` + "\n" + entries, "entry_count"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tapeformat.ReadStream(strings.NewReader(c.raw))
			if err == nil {
				t.Fatalf("ReadStream accepted %q", c.raw)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q — it may be a downstream check firing instead", err, c.want)
			}
		})
	}
}

func TestGoldenTapes_AreCommittedAndDistinct(t *testing.T) {
	want := []string{"broken-link.tape", "truncated.tape", "well-formed.tape"}
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("testdata holds %v, want exactly %v", got, want)
	}
	seen := map[string]string{}
	for _, n := range want {
		b := string(golden(t, n))
		if other, dup := seen[b]; dup {
			t.Errorf("%s and %s are byte-identical, so one of them tests nothing", n, other)
		}
		seen[b] = n
	}
}

// ---------------------------------------------------------------------------
// Reading only
// ---------------------------------------------------------------------------

// ⚠️ THE SEAM, ENFORCED. Publication authority is concentrated in one place on
// purpose: governed refs move only through the publication protocol, under
// compare-and-swap. A writer exported from a library everybody imports would put
// that surface in every consumer's hands, to no reader's benefit — a verifier
// needs to read a tape and never to write one.
func TestPackage_ExportsNoWriteOperation(t *testing.T) {
	fset := token.NewFileSet()
	examined := 0
	forbidden := []string{"Write", "Append", "Commit", "Publish", "Advance", "Truncate", "Delete"}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		examined++
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			for _, word := range forbidden {
				if strings.HasPrefix(fn.Name.Name, word) {
					t.Errorf("%s exports %s. The write path stays in the private runtime: publication "+
						"authority is concentrated in one place, and a writer here hands that surface "+
						"to every consumer for no reader's benefit.", name, fn.Name.Name)
				}
			}
		}
	}
	// ⚠️ Count-asserted: a walk that examined nothing would report no writers.
	if examined == 0 {
		t.Fatal("examined no source files, so this check passed by looking at nothing")
	}
	t.Logf("examined %d source file(s) for exported write operations", examined)
}

// Blank lines between records are tolerated, because a tape may be concatenated
// or reflowed in transit and a blank line carries no claim.
func TestReadStream_TolerantOfBlankLinesButNotOfMissingOnes(t *testing.T) {
	raw := strings.ReplaceAll(string(golden(t, "well-formed.tape")), "\n", "\n\n")
	entries, err := tapeformat.ReadStream(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("blank lines between records were refused: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("read %d entries, want 3", len(entries))
	}
}

// ⚠️ Two records on ONE line must not decode as the first. A reader that
// silently dropped the remainder would report a partial tape as a whole one —
// and the declared count would still add up, because the second record was
// never counted.
func TestReadStream_RefusesTwoRecordsOnOneLine(t *testing.T) {
	lines := strings.Split(strings.TrimRight(string(golden(t, "well-formed.tape")), "\n"), "\n")
	joined := lines[0] + "\n" + lines[1] + lines[2] + "\n" + lines[3] + "\n"
	if _, err := tapeformat.ReadStream(strings.NewReader(joined)); err == nil {
		t.Fatal("two records on one line were read as one")
	}
}

// ⚠️ An unbounded line would let a crafted stream decide this process's memory,
// so the reader caps it — and the cap must produce an ERROR, not a short read
// that looks like a shorter tape.
func TestReadStream_RefusesAnOverlongLine(t *testing.T) {
	huge := `{"schema_version":"1","format":"arqtos.tape.v1","entry_count":"1"}` + "\n" +
		`{"schema_version":"` + strings.Repeat("x", 2*1024*1024) + `"}` + "\n"
	_, err := tapeformat.ReadStream(strings.NewReader(huge))
	if err == nil {
		t.Fatal("a line past the reader's cap was accepted")
	}
	if strings.Contains(err.Error(), "declares 1 entries and the stream carries 0") {
		t.Errorf("an overlong line was reported as truncation rather than as a read failure: %v", err)
	}
}
