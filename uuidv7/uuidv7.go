// Package uuidv7 is the pure UUIDv7 function records and the ledger share
// (RFC 9562). It is offline and carries no authority: the published record
// contract is the authority (doc-arq-00013 §1.1).
package uuidv7

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// VariantRFC4122 is the RFC 4122/9562 variant (binary 10).
const VariantRFC4122 = 2

// An ID is a 16-byte UUIDv7.
type ID [16]byte

var (
	mu     sync.Mutex
	lastMS uint64
	seq    uint16
)

var errNotV7 = errors.New("uuidv7: not a UUIDv7")

// New returns a UUIDv7. Within one process, successive values are monotonic
// even when they share a millisecond.
func New() ID {
	var randB [8]byte
	if _, err := rand.Read(randB[:]); err != nil {
		panic("uuidv7: crypto/rand: " + err.Error())
	}

	ms := uint64(time.Now().UnixMilli())
	mu.Lock()
	if ms <= lastMS {
		seq++
		if seq > 0x0fff {
			lastMS++
			seq = 0
		}
		ms = lastMS
	} else {
		lastMS = ms
		seq = binary.BigEndian.Uint16(randB[6:8]) & 0x0fff
	}
	seqCopy := seq
	mu.Unlock()

	var id ID
	id[0] = byte(ms >> 40)
	id[1] = byte(ms >> 32)
	id[2] = byte(ms >> 24)
	id[3] = byte(ms >> 16)
	id[4] = byte(ms >> 8)
	id[5] = byte(ms)
	id[6] = 0x70 | byte(seqCopy>>8)
	id[7] = byte(seqCopy)
	copy(id[8:], randB[:8])
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

// Version is the RFC version nibble; New always returns 7.
func (id ID) Version() int { return int(id[6] >> 4) }

// Variant is the RFC variant; New always returns VariantRFC4122.
func (id ID) Variant() int { return int(id[8] >> 6) }

// String is the 8-4-4-4-12 hex spelling.
func (id ID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], id[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], id[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], id[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], id[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], id[10:16])
	return string(b[:])
}

// Parse reads a 8-4-4-4-12 spelling and refuses anything that is not version 7
// with the RFC 4122 variant.
func Parse(s string) (ID, error) {
	var id ID
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return id, fmt.Errorf("%w: %q", errNotV7, s)
	}
	var hexed [32]byte
	copy(hexed[0:8], s[0:8])
	copy(hexed[8:12], s[9:13])
	copy(hexed[12:16], s[14:18])
	copy(hexed[16:20], s[19:23])
	copy(hexed[20:32], s[24:36])
	n, err := hex.Decode(id[:], hexed[:])
	if err != nil || n != 16 {
		return ID{}, fmt.Errorf("%w: %q", errNotV7, s)
	}
	if id.Version() != 7 || id.Variant() != VariantRFC4122 {
		return ID{}, fmt.Errorf("%w: %q", errNotV7, s)
	}
	return id, nil
}
