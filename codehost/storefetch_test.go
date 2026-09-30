package codehost_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

func TestKnownCapabilities_CarriesStoreFetch(t *testing.T) {
	got := codehost.KnownCapabilities()
	if !got.Has(connector.Capability("store_fetch")) {
		t.Fatal("store_fetch is not registered; CloneRepo is checkout and exact_history is pinned empty-dest acquisition")
	}
}

func storeRequest() codehost.StoreFetchRequest {
	return codehost.StoreFetchRequest{
		Realm:    "host-a",
		NativeID: "424242",
		StoreDir: "/unused/registered-bare-store",
	}
}

func TestStoreFetchRequest_InvalidInputIsTyped(t *testing.T) {
	for name, mutate := range map[string]func(*codehost.StoreFetchRequest){
		"empty realm":         func(r *codehost.StoreFetchRequest) { r.Realm = "" },
		"token in realm":      func(r *codehost.StoreFetchRequest) { r.Realm = "https://user:pass@github.com" },
		"empty identity":      func(r *codehost.StoreFetchRequest) { r.NativeID = "" },
		"path identity":       func(r *codehost.StoreFetchRequest) { r.NativeID = "owner/repo" },
		"missing store":       func(r *codehost.StoreFetchRequest) { r.StoreDir = "" },
		"relative store":      func(r *codehost.StoreFetchRequest) { r.StoreDir = "repo.git" },
		"root store":          func(r *codehost.StoreFetchRequest) { r.StoreDir = "/" },
		"url store":           func(r *codehost.StoreFetchRequest) { r.StoreDir = "https://github.com/owner/repo.git" },
		"unclean store":       func(r *codehost.StoreFetchRequest) { r.StoreDir = "/tmp/../repo.git" },
		"session-tree shaped": func(r *codehost.StoreFetchRequest) { r.StoreDir = "trees/main/docs" },
	} {
		t.Run(name, func(t *testing.T) {
			r := storeRequest()
			mutate(&r)
			err := r.Validate()
			if cerr.KindOf(err) != cerr.KindInvalid {
				t.Fatalf("Validate() kind = %s, want %s", cerr.KindOf(err), cerr.KindInvalid)
			}
		})
	}
}

func TestStoreFetchRequest_HasNoURLField(t *testing.T) {
	typ := reflect.TypeOf(codehost.StoreFetchRequest{})
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		names = append(names, f.Name)
		if strings.Contains(strings.ToLower(f.Name), "url") {
			t.Errorf("field %s would admit a locator with an embedded token", f.Name)
		}
	}
	want := []string{"Realm", "NativeID", "StoreDir"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("fields = %v, want %v — identity plus registered store, never a clone URL", names, want)
	}
}

func TestStoreFetchResult_MustBindRequest(t *testing.T) {
	req := storeRequest()
	good := codehost.StoreFetchResult(req)
	if err := good.Validate(req); err != nil {
		t.Fatalf("bound receipt refused: %v", err)
	}
	for name, mutate := range map[string]func(*codehost.StoreFetchResult){
		"realm": func(x *codehost.StoreFetchResult) { x.Realm = "other" },
		"id":    func(x *codehost.StoreFetchResult) { x.NativeID = "other" },
		"store": func(x *codehost.StoreFetchResult) { x.StoreDir = "/other" },
	} {
		t.Run(name, func(t *testing.T) {
			got := good
			mutate(&got)
			if cerr.KindOf(got.Validate(req)) != cerr.KindContractViolation {
				t.Fatalf("mismatched receipt kind = %s, want %s", cerr.KindOf(got.Validate(req)), cerr.KindContractViolation)
			}
		})
	}
}

func TestStoreFetch_IsNotCloneRepoOrExactHistory(t *testing.T) {
	if codehost.CapStoreFetch == codehost.CapExactHistory || codehost.CapStoreFetch == "file_read" {
		t.Fatal("store_fetch must be a separate optional capability")
	}
	if string(codehost.CapStoreFetch) != "store_fetch" {
		t.Fatalf("wire value = %q, want store_fetch", codehost.CapStoreFetch)
	}
}
