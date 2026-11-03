package proc

import (
	"strings"
	"testing"
)

func TestDeduce(t *testing.T) {
	in := &Info{
		Proc:    Process{PID: 9, Name: "frobnicated", Path: "/System/Library/PrivateFrameworks/Frob.framework/Support/frobnicated", Kind: KindSystem},
		Job:     &Job{Label: "com.apple.frobnicated", Domain: "system daemon", OnDemand: []string{"XPC request"}},
		Lineage: []Process{{PID: 1, Name: "launchd"}},
	}
	got := strings.Join(Deduce(in), "\n")
	for _, want := range []string{"Frob framework", "background daemon", "starts on demand: XPC request"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "started by launchd") {
		t.Errorf("launchd parent should be omitted:\n%s", got)
	}
	in.Lineage = []Process{{PID: 1, Name: "launchd"}, {PID: 77, Name: "Foo"}}
	if got := strings.Join(Deduce(in), "\n"); !strings.Contains(got, "started by Foo (PID 77)") {
		t.Errorf("parent clue missing:\n%s", got)
	}
}

func TestFindUnexplained(t *testing.T) {
	ps := []Process{
		{PID: 1, Name: "zzz-unknown"}, {PID: 2, Name: "zzz-unknown"},
		{PID: 3, Name: "sleep"}, {PID: 4, Name: "aaa-unknown"},
	}
	got := FindUnexplained(ps)
	if len(got) != 2 || got[0].Name != "aaa-unknown" || got[1].Count != 2 {
		t.Fatalf("got %+v", got)
	}
}
