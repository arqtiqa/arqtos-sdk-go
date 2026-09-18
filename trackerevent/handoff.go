// Package trackerevent is the provider-neutral handoff a Tracker webhook
// adapter produces for a host receiver.
//
// It is not a connector class, not a sixth Tracker method, and not CodeHost
// webhook registration. The adapter authenticates and normalizes; the host
// owns durable inbox, dedupe processing, acknowledgement and business-event
// derivation.
//
// repoobs.Envelope is a WorkEvent wrapper that requires occurred_at.
// This handoff keeps unknown times unknown. codehost.CapWebhooks registers a
// repository hook; it is not this surface.
package trackerevent

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
)

const (
	SchemaVersion = 1
	MaxBody       = 1 << 20
	op            = "trackerevent"
)

// Instant is a time that may be unknown. The zero value is unknown, never Unix epoch.
type Instant struct {
	Known bool      `json:"known"`
	Time  time.Time `json:"time,omitempty"`
}

type Entity struct {
	Scope  string `json:"scope,omitempty"`
	Number int    `json:"number,omitempty"`
	Native string `json:"native,omitempty"`
}

type Handoff struct {
	SchemaVersion  int     `json:"schema_version"`
	Provider       string  `json:"provider"`
	Realm          string  `json:"realm"`
	Workspace      string  `json:"workspace"`
	EventID        string  `json:"event_id"`
	DeliveryID     string  `json:"delivery_id"`
	Kind           string  `json:"kind"`
	PayloadVersion string  `json:"payload_version"`
	Occurred       Instant `json:"occurred"`
	Observed       Instant `json:"observed"`
	Entity         Entity  `json:"entity"`
}

func (h Handoff) DedupeKey() string {
	return h.Provider + "|" + h.Realm + "|" + h.Workspace + "|" + h.EventID
}

func (h Handoff) Validate() error {
	if h.SchemaVersion != SchemaVersion {
		return cerr.New(cerr.KindUnsupported, op, errors.New("unsupported schema version"))
	}
	for _, f := range []struct{ name, value string }{
		{"provider", h.Provider},
		{"realm", h.Realm},
		{"workspace", h.Workspace},
		{"event_id", h.EventID},
		{"delivery_id", h.DeliveryID},
		{"kind", h.Kind},
		{"payload_version", h.PayloadVersion},
	} {
		if strings.TrimSpace(f.value) == "" {
			return cerr.New(cerr.KindInvalid, op, errors.New("missing "+f.name))
		}
	}
	return nil
}

func Decode(raw []byte) (Handoff, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var h Handoff
	if err := dec.Decode(&h); err != nil {
		return Handoff{}, cerr.New(cerr.KindInvalid, op, err)
	}
	return h, nil
}

type Raw struct {
	PayloadVersion string
	Signature      string
	Body           []byte
}

type Verified struct {
	PayloadVersion string
	Body           []byte
}

func Bind(v Verified, h Handoff) error {
	if v.PayloadVersion != h.PayloadVersion {
		return cerr.New(cerr.KindUnsupported, op, errors.New("payload version does not match verified input"))
	}
	return h.Validate()
}

func CheckBodySize(body []byte) error {
	if len(body) > MaxBody {
		return cerr.New(cerr.KindInvalid, op, errors.New("body exceeds size bound"))
	}
	return nil
}
