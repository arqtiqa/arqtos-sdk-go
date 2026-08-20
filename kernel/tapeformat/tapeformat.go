package tapeformat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/arqtiqa/arqtos-sdk-go/contracts"
	"github.com/arqtiqa/arqtos-sdk-go/kernel/canonical"
)

// SchemaVersion is the version every tape record carries, as a string.
//
// ⚠️ A string because these records are canonically encoded and the canonical
// form forbids JSON numbers — two encoders can disagree about how a number is
// written while agreeing it is the same number, and a record whose digest
// matters cannot afford that.
const SchemaVersion = "1"

// StreamFormat is the format tag a serialized tape declares.
//
// ⚠️ It is checked rather than assumed. A reader that interpreted an untagged
// byte stream would be reading under a contract nobody wrote down.
const StreamFormat = "arqtos.tape.v1"

var (
	// ErrInvalidEntry is a tape entry that does not say where it sits or what
	// it records.
	ErrInvalidEntry = errors.New("tapeformat: the tape entry is unusable")

	// ErrBrokenChain is a sequence of entries that does not form one chain.
	ErrBrokenChain = errors.New("tapeformat: the entries do not form a chain")

	// ErrTruncated is a stream that ended before the entries it declared.
	//
	// ⚠️ Distinct from ErrBrokenChain, and the distinction is the point: a
	// truncated tape is a COMPLETE, VALID, SHORTER chain. Nothing about the
	// entries themselves is wrong, which is exactly why truncation cannot be
	// detected by looking at them and needs a declared count.
	ErrTruncated = errors.New("tapeformat: the tape ended before the entries it declares")
)

// An Entry is one accepted act's position on the tape.
//
// ⚠️ It carries no identity of its own. An entry IS its act, so the act's body
// id is the identity, and the chain links by that. A separate entry id would be
// a second name for one thing, and the two would eventually disagree.
type Entry struct {
	SchemaVersion string `json:"schema_version"`

	// Sequence is the position, decimal, from "0" and increasing by one.
	//
	// ⚠️ A string for the canonical rule, but it must still BE a number: the
	// ordering the whole tape depends on is numeric, and a string sort puts
	// entry 10 before entry 2.
	Sequence string `json:"sequence"`

	// ActBodyID is the act at this position.
	ActBodyID string `json:"act_body_id"`

	// ParentActBodyID is the act at the previous position. Empty ONLY at
	// sequence 0 — the one entry nothing precedes.
	ParentActBodyID string `json:"parent_act_body_id"`

	// AcceptedAt is when the act was accepted, from a named time authority.
	AcceptedAt contracts.AcceptedTime `json:"accepted_at"`
}

// SequenceNumber returns e's position as a number.
func (e Entry) SequenceNumber() (int, error) {
	n, err := strconv.Atoi(e.Sequence)
	switch {
	case err != nil:
		return 0, fmt.Errorf("%w: sequence %q is not a number", ErrInvalidEntry, e.Sequence)
	case n < 0:
		return 0, fmt.Errorf("%w: sequence %d is negative", ErrInvalidEntry, n)
	// ⚠️ A padded number is refused rather than parsed. "007" and "7" would be
	// two spellings of one position, and these bytes are digested.
	case strconv.Itoa(n) != e.Sequence:
		return 0, fmt.Errorf("%w: sequence %q is not the canonical spelling of %d", ErrInvalidEntry, e.Sequence, n)
	}
	return n, nil
}

// Validate reports whether e says where it sits and what it records.
func (e Entry) Validate() error {
	var problems []string
	if strings.TrimSpace(e.SchemaVersion) == "" {
		problems = append(problems, "no schema version")
	}
	if strings.TrimSpace(e.Sequence) == "" {
		problems = append(problems, "no sequence")
	} else if _, err := e.SequenceNumber(); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(e.ActBodyID) == "" {
		problems = append(problems, "no act — an entry that records nothing is a position with no content")
	}
	if err := e.AcceptedAt.Validate(); err != nil {
		problems = append(problems, "acceptance time: "+err.Error())
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidEntry, strings.Join(problems, "; "))
}

// Digest is the domain-separated digest of e's canonical bytes.
//
// ⚠️ It uses the evidence-event domain because a tape entry IS an
// append-only record of an acceptance. Reusing an existing tag rather than
// minting one keeps the closed set closed: the encoding is owned forever, and a
// tag added casually is a tag that must be honoured forever.
func (e Entry) Digest() (string, error) { return canonical.Digest(canonical.DomainEvidenceEvent, e) }

// VerifyChain reports whether entries form one chain: positions from zero
// without gaps, each entry linking to its predecessor, and no act twice.
//
// ⚠️ It cannot detect TRUNCATION, and that is not a gap in it. A tape cut short
// at an entry boundary is a complete, valid, shorter chain — see [ReadStream],
// where the declared count is what makes the difference visible.
func VerifyChain(entries []Entry) error {
	if len(entries) == 0 {
		return fmt.Errorf("%w: the tape is empty; a chain with no first entry is not a chain", ErrBrokenChain)
	}

	seen := make(map[string]int, len(entries))
	for i, e := range entries {
		if err := e.Validate(); err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
		n, err := e.SequenceNumber()
		if err != nil {
			return fmt.Errorf("entry %d: %w", i, err)
		}
		if n != i {
			return fmt.Errorf("%w: entry %d is at sequence %d; positions run from zero without gaps", ErrBrokenChain, i, n)
		}
		if prev, dup := seen[e.ActBodyID]; dup {
			return fmt.Errorf("%w: entry %d records act %s, already at entry %d — one act has one position, "+
				"and an act at two would let a spend be replayed by re-listing it",
				ErrBrokenChain, i, e.ActBodyID, prev)
		}
		seen[e.ActBodyID] = i

		if i == 0 {
			if e.ParentActBodyID != "" {
				return fmt.Errorf("%w: the first entry names parent %s; nothing precedes the first entry, "+
					"so this tape's start is somewhere it cannot show", ErrBrokenChain, e.ParentActBodyID)
			}
			continue
		}
		if e.ParentActBodyID == "" {
			return fmt.Errorf("%w: entry %d names no parent, and only the first entry may", ErrBrokenChain, i)
		}
		if e.ParentActBodyID != entries[i-1].ActBodyID {
			return fmt.Errorf("%w: entry %d links to %s, but its predecessor is %s",
				ErrBrokenChain, i, e.ParentActBodyID, entries[i-1].ActBodyID)
		}
	}
	return nil
}

// A streamHeader opens a serialized tape and declares how many entries follow.
//
// ⚠️ EntryCount exists for exactly one reason: without it, a tape cut short at
// an entry boundary is indistinguishable from a shorter complete tape. That is
// the failure that most reads like success, and no amount of checking the
// entries themselves can find it.
type streamHeader struct {
	SchemaVersion string `json:"schema_version"`
	Format        string `json:"format"`
	EntryCount    string `json:"entry_count"`
}

// ReadStream reads a serialized tape: a header line declaring the entry count,
// then exactly that many entry lines.
//
// ⚠️ Hostile input throughout. Unknown fields are refused, a short read is
// refused rather than returned as a shorter tape, trailing content is refused
// rather than ignored, and the entries are chain-checked before they are
// returned. A reader that returned what it managed to parse would report a
// partial tape as a whole one.
func ReadStream(r io.Reader) ([]Entry, error) {
	// A generous cap: an entry is small, and an unbounded line would let a
	// crafted stream decide this process's memory.
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("tapeformat: reading the header: %w", err)
		}
		return nil, fmt.Errorf("%w: the stream is empty, so it declares nothing", ErrTruncated)
	}
	var h streamHeader
	if err := decodeStrict(sc.Bytes(), &h); err != nil {
		return nil, fmt.Errorf("tapeformat: the header: %w", err)
	}
	if h.Format != StreamFormat {
		return nil, fmt.Errorf("tapeformat: the stream declares format %q, want %q", h.Format, StreamFormat)
	}
	want, err := strconv.Atoi(h.EntryCount)
	if err != nil || want < 0 || strconv.Itoa(want) != h.EntryCount {
		return nil, fmt.Errorf("tapeformat: the header declares entry_count %q, which is not a count", h.EntryCount)
	}

	entries := make([]Entry, 0, min(want, 1024))
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if len(entries) == want {
			return nil, fmt.Errorf("tapeformat: the stream carries more entries than the %d it declares", want)
		}
		var e Entry
		if err := decodeStrict(line, &e); err != nil {
			return nil, fmt.Errorf("tapeformat: entry %d: %w", len(entries), err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("tapeformat: reading entries: %w", err)
	}

	// ⚠️ THE COUNT ASSERTION. Everything read so far can be perfectly valid and
	// still be half a tape.
	if len(entries) != want {
		return nil, fmt.Errorf("%w: the header declares %d entries and the stream carries %d",
			ErrTruncated, want, len(entries))
	}
	if err := VerifyChain(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// decodeStrict decodes one JSON value, refusing unknown fields and anything
// after it.
func decodeStrict(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing content after the record")
	}
	return nil
}
