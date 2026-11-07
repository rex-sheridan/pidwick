package proc

import (
	"strings"
	"testing"
)

func TestParseThreadsGroupsPools(t *testing.T) {
	out := `"main" #3 [1] prio=5
   java.lang.Thread.State: RUNNABLE
"async-dispatch-1" #20 [2] daemon prio=5
   java.lang.Thread.State: WAITING
"async-dispatch-2" #21 [3] daemon prio=5
   java.lang.Thread.State: WAITING
"nREPL-session-3f2a9c1e-1b2c-4d5e-8f90-a1b2c3d4e5f6" #30 [4]
   java.lang.Thread.State: TIMED_WAITING
`
	var j JVM
	j.parseThreads(out)
	if j.Threads != 4 {
		t.Fatalf("threads = %d", j.Threads)
	}
	if g := j.ThreadGroups[0]; g.Name != "async-dispatch" || g.Count != 2 {
		t.Errorf("top group = %+v", g)
	}
	var found bool
	for _, g := range j.ThreadGroups {
		found = found || g.Name == "nREPL-session-*"
	}
	if !found {
		t.Errorf("uuid not collapsed: %+v", j.ThreadGroups)
	}
	if j.ThreadStates["WAITING"] != 2 || j.Deadlock != "" {
		t.Errorf("states = %v deadlock=%q", j.ThreadStates, j.Deadlock)
	}
	j.parseThreads("Found one Java-level deadlock:\n")
	if j.Deadlock == "" {
		t.Error("deadlock not detected")
	}
}

func TestParseHeapAndFlags(t *testing.T) {
	var j JVM
	j.parseFlags("123:\n-XX:+UseG1GC -XX:MaxHeapSize=4294967296 -XX:InitialHeapSize=268435456\n")
	j.parseHeap("123:\n garbage-first heap   total reserved 4194304K, committed 911360K, used 151786K [0x1, 0x2)\n region size 2048K\n")
	if j.GC != "G1" || j.HeapMaxKB != 4194304 || j.HeapCommKB != 911360 || j.HeapUsedKB != 151786 {
		t.Errorf("%+v", j)
	}
	// older JDK format, no max flag
	var k JVM
	k.parseHeap(" def new generation   total 100K, used 50K [..]\n Heap total 900K, used 400K\n")
	if k.HeapUsedKB == 0 {
		t.Errorf("old format not parsed: %+v", k)
	}
}

func TestParseJstat(t *testing.T) {
	m := parseJstat("  S0     S1     E      O      M     CCS    YGC     YGCT     FGC    FGCT     GCT\n     -  57.51  13.53  20.48  82.18  76.51     83     0.235     0     0.000     0.243\n")
	if m["YGC"] != "83" || m["GCT"] != "0.243" || m["M"] != "82.18" {
		t.Errorf("%v", m)
	}
	if parseJstat("garbage") != nil {
		t.Error("expected nil")
	}
}

func TestFindEndpoints(t *testing.T) {
	l := Launch{
		Props:   [][2]string{{"clojure.server.repl", "{:port 5555 :accept clojure.core.server/repl}"}},
		Options: []string{"-agentlib:jdwp=transport=dt_socket,server=y,address=*:5005"},
	}
	h := Handles{Conns: []Conn{{State: "LISTEN", Local: "127.0.0.1:5555", Scope: "loopback"}}}
	eps := findEndpoints(l, h, t.TempDir())
	if len(eps) != 2 {
		t.Fatalf("%v", eps)
	}
	if eps[0].Port != "5005" || eps[0].Open || eps[1].Port != "5555" || !eps[1].Open {
		t.Errorf("%+v", eps)
	}
}

func TestClojureVersion(t *testing.T) {
	cp := []string{"src", "/m2/org/clojure/clojure/1.12.6/clojure-1.12.6.jar"}
	if v := ClojureVersion(cp); v != "1.12.6" {
		t.Errorf("got %q", v)
	}
}

func TestAttachFailureMessage(t *testing.T) {
	msg := attachFailure("1:\ncom.sun.tools.attach.AttachNotSupportedException: pid: 1, state is not ready to participate in attach handshake!\n", nil)
	if msg == "" || msg[:3] != "The" {
		t.Errorf("%q", msg)
	}
}

func TestTrimJcmdHeader(t *testing.T) {
	if got := trimJcmdHeader("1234:\nfoo\nbar\n"); got != "foo\nbar" {
		t.Errorf("%q", got)
	}
	if got := trimJcmdHeader("no header here\n"); got != "no header here" {
		t.Errorf("%q", got)
	}
}

func TestTrimHistogram(t *testing.T) {
	in := " num     #instances         #bytes  class name (module)\n-------------------------------------------------------\n   1:           100           9000  [B\n   2:            50           800  java.lang.String\n   3:            10           100  java.lang.Object\nTotal           160           9900\n"
	got := trimHistogram(in, 2)
	for _, want := range []string{"[B", "java.lang.String", "1 more classes", "Total"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "java.lang.Object") {
		t.Errorf("third entry should be cut:\n%s", got)
	}
}
