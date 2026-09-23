package codehostconform_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arqtiqa/arqtos-sdk-go/cerr"
	"github.com/arqtiqa/arqtos-sdk-go/codehost"
	"github.com/arqtiqa/arqtos-sdk-go/codehostconform"
	"github.com/arqtiqa/arqtos-sdk-go/connector"
)

type historyStub struct { *stub; fault string }

func (s historyStub) AcquireExact(ctx context.Context, r codehost.ExactHistoryRequest) (codehost.ExactHistoryResult, error) {
	var zero codehost.ExactHistoryResult
	if ctx.Err()!=nil {
		if s.fault=="cancel" { return zero, ctx.Err() }
		return zero, cerr.New(cerr.KindTimeout,"AcquireExact",ctx.Err())
	}
	if err:=r.Validate(); err!=nil {
		if s.fault=="invalid" { return zero,nil }
		return zero,err
	}
	if strings.HasPrefix(string(r.Expected),"b") {
		if s.fault=="race" { return zero,cerr.New(cerr.KindInvalid,"AcquireExact",errors.New("unrelated bad input")) }
		return zero,cerr.New(cerr.KindInvalid,"AcquireExact",codehost.ErrRefChanged)
	}
	x:=codehost.ExactHistoryResult{Realm:r.Realm,NativeID:r.NativeID,Ref:r.Ref,ObjectID:r.Expected,DestinationDir:r.DestinationDir,Complete:true}
	if s.fault=="receipt" { x.ObjectID=codehost.ObjectID(strings.Repeat("c",40)) }
	if s.fault=="closure" { x.Complete=false }
	return x,nil
}

func historyOptions(t *testing.T) codehostconform.Options {
	t.Helper()
	request:=func(dir string) codehost.ExactHistoryRequest { return codehost.ExactHistoryRequest{Realm:"host-a",NativeID:"1",Ref:"refs/heads/main",Expected:codehost.ObjectID(strings.Repeat("a",40)),DestinationDir:filepath.Join(t.TempDir(),dir)} }
	stale:=request("stale"); stale.Expected=codehost.ObjectID(strings.Repeat("b",40))
	return codehostconform.Options{Manifest:stubManifest(codehost.CapExactHistory), ListableOwner:fixtureListable, UnlistableOwner:fixtureUnlistable, MissingOwner:fixtureMissingOwner, ExactHistory:&codehostconform.ExactHistoryFixtures{Request:request("good"),StaleRequest:stale,CanceledRequest:request("canceled")}}
}

func TestConform_ExactHistoryRejectsBadBehavior(t *testing.T) {
	for _, fault:=range []string{"","receipt","closure","cancel","race","invalid"} {
		t.Run(fault,func(t *testing.T){
			s:=newStub(); s.caps=connector.Capabilities{codehost.CapExactHistory}
			rep,err:=codehostconform.Run(context.Background(),historyStub{s,fault},historyOptions(t)); if err!=nil { t.Fatal(err) }
			if fault=="" { if !rep.OK() { t.Fatal(rep.String()) }; return }
			if rep.OK() { t.Fatalf("conformance accepted %s defect",fault) }
		})
	}
}

func TestConform_ExactHistoryRequiresFixturesAndCapability(t *testing.T) {
	s:=newStub(); s.caps=connector.Capabilities{codehost.CapExactHistory}
	opts:=historyOptions(t)
	rep,err:=codehostconform.Run(context.Background(),s,opts); if err!=nil { t.Fatal(err) }; requireFailed(t,rep,codehostconform.CheckOptionalDeclared)
	s.caps=nil; opts.Manifest=stubManifest()
	rep,err=codehostconform.Run(context.Background(),historyStub{s,""},opts); if err!=nil { t.Fatal(err) }; requireFailed(t,rep,codehostconform.CheckOptionalDeclared)
	s.caps=connector.Capabilities{codehost.CapExactHistory}; opts=historyOptions(t); opts.ExactHistory=nil
	if _,err:=codehostconform.Run(context.Background(),historyStub{s,""},opts); cerr.KindOf(err)!=cerr.KindInvalid { t.Fatalf("missing fixtures: %v",err) }
	opts=historyOptions(t); opts.ExactHistory.StaleRequest.DestinationDir=opts.ExactHistory.Request.DestinationDir
	if _,err:=codehostconform.Run(context.Background(),historyStub{s,""},opts); cerr.KindOf(err)!=cerr.KindInvalid { t.Fatalf("shared destinations: %v",err) }
}
