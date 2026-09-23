package codehost_test

import (
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

func TestExactHistory_CapabilityIsRegistered(t *testing.T) {
	if !codehost.KnownCapabilities().Has(connector.Capability("exact_history")) {
		t.Fatal("exact_history is not registered")
	}
}

func historyRequest() codehost.ExactHistoryRequest {
	return codehost.ExactHistoryRequest{Realm: "host-a", NativeID: "123", Ref: "refs/heads/main", Expected: codehost.ObjectID(strings.Repeat("a", 40)), DestinationDir: "/unused/empty-repository"}
}

func TestExactHistoryRequest_InvalidInputIsTyped(t *testing.T) {
	for name, mutate := range map[string]func(*codehost.ExactHistoryRequest){
		"realm": func(r *codehost.ExactHistoryRequest) { r.Realm = "https://user:pass@host" },
		"identity": func(r *codehost.ExactHistoryRequest) { r.NativeID = "../repo" },
		"ref": func(r *codehost.ExactHistoryRequest) { r.Ref = "HEAD" },
		"missing oid": func(r *codehost.ExactHistoryRequest) { r.Expected = "" },
		"short oid": func(r *codehost.ExactHistoryRequest) { r.Expected = "abcd" },
		"zero oid": func(r *codehost.ExactHistoryRequest) { r.Expected = codehost.ObjectID(strings.Repeat("0", 40)) },
		"missing destination": func(r *codehost.ExactHistoryRequest) { r.DestinationDir = "" },
		"relative destination": func(r *codehost.ExactHistoryRequest) { r.DestinationDir = "repo" },
		"root destination": func(r *codehost.ExactHistoryRequest) { r.DestinationDir = "/" },
		"unclean destination": func(r *codehost.ExactHistoryRequest) { r.DestinationDir = "/tmp/../repo" },
	} {
		t.Run(name, func(t *testing.T) {
			r := historyRequest()
			mutate(&r)
			if err := r.Validate(); cerr.KindOf(err) != cerr.KindInvalid {
				t.Fatalf("got %v, want invalid", err)
			}
		})
	}
	for _, width := range []int{40, 64} {
		r := historyRequest()
		r.Expected = codehost.ObjectID(strings.Repeat("b", width))
		if err := r.Validate(); err != nil {
			t.Fatalf("syntax-only validation must not inspect disk: %v", err)
		}
	}
}

func TestExactHistoryResult_MustBindCompleteRequest(t *testing.T) {
	r := historyRequest()
	good := codehost.ExactHistoryResult{Realm:r.Realm, NativeID:r.NativeID, Ref:r.Ref, ObjectID:r.Expected, DestinationDir:r.DestinationDir, Complete:true}
	if err := good.Validate(r); err != nil { t.Fatal(err) }
	for name, mutate := range map[string]func(*codehost.ExactHistoryResult){
		"realm":func(x *codehost.ExactHistoryResult){ x.Realm="other" },
		"identity":func(x *codehost.ExactHistoryResult){ x.NativeID="other" },
		"ref":func(x *codehost.ExactHistoryResult){ x.Ref="refs/heads/other" },
		"oid":func(x *codehost.ExactHistoryResult){ x.ObjectID=codehost.ObjectID(strings.Repeat("b",40)) },
		"destination":func(x *codehost.ExactHistoryResult){ x.DestinationDir="/other" },
		"incomplete":func(x *codehost.ExactHistoryResult){ x.Complete=false },
	} {
		t.Run(name,func(t *testing.T){ x:=good; mutate(&x); if err:=x.Validate(r); cerr.KindOf(err)!=cerr.KindContractViolation { t.Fatalf("got %v, want contract violation",err) } })
	}
	r.Expected=""
	if err:=good.Validate(r); cerr.KindOf(err)!=cerr.KindInvalid { t.Fatalf("bad request: %v",err) }
}
