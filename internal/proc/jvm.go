package proc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// JVM is live information from a running Java process, gathered by attaching
// with the JDK's own jcmd and jstat. Attaching is read-only but not free: the
// JVM briefly reaches a safepoint to produce a thread dump.
type JVM struct {
	Tool     string // jcmd binary used
	Err      string // why attaching failed, if it did
	Gathered time.Time

	Version string // "JDK 25.0.4"
	VMName  string // "Java HotSpot(TM) 64-Bit Server VM version 25.0.4+7-LTS"
	Uptime  time.Duration

	GC          string // "G1", "Parallel", "ZGC", ...
	HeapUsedKB  int64
	HeapCommKB  int64
	HeapMaxKB   int64 // from -XX:MaxHeapSize
	HeapInitKB  int64
	Collections map[string]string // jstat -gcutil columns (YGC, YGCT, FGC, ...)
	GCTime      time.Duration     // total time spent in GC
	Flags       []string          // JVM flags in effect (non-default)

	Threads      int
	ThreadStates map[string]int // RUNNABLE, WAITING, ...
	ThreadGroups []ThreadGroup
	Deadlock     string // non-empty when the JVM reports a deadlock

	Endpoints []Endpoint
}

type ThreadGroup struct {
	Name  string
	Count int
}

// Endpoint is a port the JVM exposes for tooling (REPL, debugger, JMX).
type Endpoint struct {
	Kind  string // "nREPL", "Clojure socket REPL", "Java debugger (JDWP)", "JMX"
	Port  string
	Open  bool   // seen listening
	Where string // "localhost only", "all interfaces", ...
	Via   string // how we know: ".nrepl-port file", "-Dclojure.server.repl", ...
}

// IsJava reports whether the process looks like a JVM we could attach to.
func IsJava(l Launch) bool { return l.Runtime == "Java" }

// jdkTool returns the named JDK tool from the same JDK as the target's java
// binary (so the attach client matches the target), else the one on PATH.
func jdkTool(javaPath, name string) string {
	if javaPath != "" {
		cand := filepath.Join(filepath.Dir(javaPath), name)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return name
}

// InspectJVM attaches to pid and collects what the JDK tools can tell us. It
// never returns nil; failures are reported in JVM.Err.
func InspectJVM(ctx context.Context, p Process, launch Launch, h Handles, cwd string) *JVM {
	j := &JVM{Gathered: time.Now()}
	if p.UID != os.Getuid() {
		j.Err = "This JVM belongs to " + p.User + ". The JDK tools can only attach to JVMs owned by the same user as Pidwick."
		return j
	}
	jcmd := jdkTool(p.Path, "jcmd")
	jstat := jdkTool(p.Path, "jstat")
	j.Tool = jcmd
	pid := strconv.Itoa(p.PID)

	var wg sync.WaitGroup
	outs := make([]string, 4)
	errs := make([]error, 4)
	cmds := [][]string{
		{jcmd, pid, "VM.version"},
		{jcmd, pid, "VM.flags"},
		{jcmd, pid, "GC.heap_info"},
		{jcmd, pid, "Thread.print"},
	}
	for i, c := range cmds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i], errs[i] = run(ctx, 8*time.Second, c[0], c[1:]...)
		}()
	}
	var stat, upt string
	var statErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		stat, statErr = run(ctx, 6*time.Second, jstat, "-gcutil", pid)
	}()
	go func() {
		defer wg.Done()
		upt, _ = run(ctx, 6*time.Second, jcmd, pid, "VM.uptime")
	}()
	wg.Wait()

	if errs[0] != nil && errs[3] != nil {
		j.Err = attachFailure(outs[0], errs[0])
		return j
	}
	j.parseVersion(outs[0])
	j.parseFlags(outs[1])
	j.parseHeap(outs[2])
	j.parseThreads(outs[3])
	j.parseUptime(upt)
	if statErr == nil {
		j.Collections = parseJstat(stat)
		if s, err := strconv.ParseFloat(j.Collections["GCT"], 64); err == nil {
			j.GCTime = time.Duration(s * float64(time.Second))
		}
	}
	j.Endpoints = findEndpoints(launch, h, cwd)
	return j
}

// attachFailure turns jcmd's stack trace into one readable sentence.
func attachFailure(out string, err error) string {
	first := ""
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasSuffix(l, ":") && !strings.Contains(l, " ") {
			continue // skip the "1234:" header
		}
		first = l
		break
	}
	switch {
	case strings.Contains(first, "not ready to participate in attach handshake"):
		return "The JVM did not answer the attach request. It is probably starting up, shutting down, hung, or started with -XX:+DisableAttachMechanism."
	case strings.Contains(first, "Permission denied") || strings.Contains(first, "AccessDenied"):
		return "Permission denied attaching to this JVM."
	case strings.Contains(first, "No such process") || strings.Contains(first, "Unable to open socket file"):
		return "Could not reach the JVM's attach socket: " + first
	case first == "" && err != nil:
		return "jcmd failed: " + err.Error()
	}
	return "jcmd failed: " + first
}

var versionRe = regexp.MustCompile(`(?m)^(Java .*? VM version .*|OpenJDK .*? VM version .*|.* VM version .*)$`)

func (j *JVM) parseVersion(out string) {
	if m := versionRe.FindString(out); m != "" {
		j.VMName = strings.TrimSpace(m)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "JDK ") {
			j.Version = strings.TrimSpace(l)
		}
	}
}

func (j *JVM) parseUptime(out string) {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if s, ok := strings.CutSuffix(l, " s"); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				j.Uptime = time.Duration(f * float64(time.Second))
			}
		}
	}
}

func (j *JVM) parseFlags(out string) {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "-XX:") && !strings.HasPrefix(l, "-") {
			continue
		}
		j.Flags = strings.Fields(l)
	}
	for _, f := range j.Flags {
		switch {
		case f == "-XX:+UseG1GC":
			j.GC = "G1"
		case f == "-XX:+UseParallelGC":
			j.GC = "Parallel"
		case f == "-XX:+UseSerialGC":
			j.GC = "Serial"
		case f == "-XX:+UseZGC":
			j.GC = "ZGC"
		case f == "-XX:+UseShenandoahGC":
			j.GC = "Shenandoah"
		case strings.HasPrefix(f, "-XX:MaxHeapSize="):
			j.HeapMaxKB = flagBytes(f) / 1024
		case strings.HasPrefix(f, "-XX:InitialHeapSize="):
			j.HeapInitKB = flagBytes(f) / 1024
		}
	}
}

func flagBytes(f string) int64 {
	_, v, _ := strings.Cut(f, "=")
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

var heapRe = regexp.MustCompile(`total (?:reserved )?(\d+)K(?:, committed (\d+)K)?, used (\d+)K`)

func (j *JVM) parseHeap(out string) {
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		low := strings.ToLower(l)
		if j.GC == "" {
			switch {
			case strings.Contains(low, "garbage-first"):
				j.GC = "G1"
			case strings.Contains(low, "zgc"):
				j.GC = "ZGC"
			case strings.Contains(low, "shenandoah"):
				j.GC = "Shenandoah"
			case strings.Contains(low, "psyounggen"):
				j.GC = "Parallel"
			case strings.Contains(low, "def new generation"):
				j.GC = "Serial"
			}
		}
	}
	for _, l := range lines {
		if !strings.Contains(strings.ToLower(l), "heap") {
			continue
		}
		if m := heapRe.FindStringSubmatch(l); m != nil {
			total, _ := strconv.ParseInt(m[1], 10, 64)
			comm := total
			if m[2] != "" {
				comm, _ = strconv.ParseInt(m[2], 10, 64)
			}
			used, _ := strconv.ParseInt(m[3], 10, 64)
			j.HeapCommKB, j.HeapUsedKB = comm, used
			if j.HeapMaxKB == 0 {
				j.HeapMaxKB = total
			}
			return
		}
	}
}

var (
	threadRe = regexp.MustCompile(`^"([^"]*)"`)
	stateRe  = regexp.MustCompile(`java\.lang\.Thread\.State: (\w+)`)
	uuidRe   = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	trailRe  = regexp.MustCompile(`[-_ #:]*\d+$`)
)

// threadGroupName collapses numbered pool members and session UUIDs, so
// "async-dispatch-3" and "async-dispatch-4" count as one group.
func threadGroupName(n string) string {
	n = uuidRe.ReplaceAllString(n, "*")
	for {
		t := trailRe.ReplaceAllString(n, "")
		if t == n || t == "" {
			break
		}
		n = t
	}
	return n
}

func (j *JVM) parseThreads(out string) {
	j.ThreadStates = map[string]int{}
	groups := map[string]int{}
	for _, l := range strings.Split(out, "\n") {
		if m := threadRe.FindStringSubmatch(l); m != nil {
			j.Threads++
			groups[threadGroupName(m[1])]++
		} else if m := stateRe.FindStringSubmatch(l); m != nil {
			j.ThreadStates[m[1]]++
		}
		if strings.Contains(l, "Found one Java-level deadlock") || strings.Contains(l, "Found Java-level deadlock") {
			j.Deadlock = "The JVM reports a Java-level deadlock. Run jcmd <pid> Thread.print to see the cycle."
		}
	}
	for n, c := range groups {
		j.ThreadGroups = append(j.ThreadGroups, ThreadGroup{n, c})
	}
	sort.Slice(j.ThreadGroups, func(a, b int) bool {
		if j.ThreadGroups[a].Count != j.ThreadGroups[b].Count {
			return j.ThreadGroups[a].Count > j.ThreadGroups[b].Count
		}
		return j.ThreadGroups[a].Name < j.ThreadGroups[b].Name
	})
}

// parseJstat reads `jstat -gcutil` (a header row and one value row).
func parseJstat(out string) map[string]string {
	var rows [][]string
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			rows = append(rows, f)
		}
	}
	if len(rows) < 2 || len(rows[0]) != len(rows[1]) {
		return nil
	}
	m := map[string]string{}
	for i, k := range rows[0] {
		m[k] = rows[1][i]
	}
	return m
}

var (
	jdwpAddrRe = regexp.MustCompile(`address=(?:[^:,]*:)?(\d+)`)
	replPortRe = regexp.MustCompile(`:port\s+(\d+)`)
)

// findEndpoints works out which listening ports are a REPL or debugger, from
// launch flags, system properties and a project's .nrepl-port file.
func findEndpoints(l Launch, h Handles, cwd string) []Endpoint {
	listening := map[string]Conn{}
	for _, c := range h.Conns {
		if c.State == "LISTEN" {
			_, port := splitHostPort(c.Local)
			listening[port] = c
		}
	}
	var eps []Endpoint
	add := func(kind, port, via string) {
		for _, e := range eps {
			if e.Port == port {
				return
			}
		}
		e := Endpoint{Kind: kind, Port: port, Via: via}
		if c, ok := listening[port]; ok {
			e.Open = true
			switch c.Scope {
			case "any":
				e.Where = "all interfaces"
			case "LAN":
				e.Where = "local network"
			default:
				e.Where = "localhost only"
			}
		}
		eps = append(eps, e)
	}

	if cwd != "" {
		if b, err := os.ReadFile(filepath.Join(cwd, ".nrepl-port")); err == nil {
			port := strings.TrimSpace(string(b))
			if _, err := strconv.Atoi(port); err == nil {
				add("nREPL", port, ".nrepl-port in the working directory")
			}
		}
	}
	for _, p := range l.Props {
		switch {
		case strings.HasPrefix(p[0], "clojure.server."):
			if m := replPortRe.FindStringSubmatch(p[1]); m != nil {
				add("Clojure socket server ("+strings.TrimPrefix(p[0], "clojure.server.")+")", m[1], "-D"+p[0])
			}
		case p[0] == "com.sun.management.jmxremote.port":
			add("JMX", p[1], "-D"+p[0])
		}
	}
	for _, o := range l.Options {
		if strings.Contains(o, "jdwp") {
			if m := jdwpAddrRe.FindStringSubmatch(o); m != nil {
				add("Java debugger (JDWP)", m[1], "-agentlib:jdwp")
			}
		}
	}
	sort.Slice(eps, func(a, b int) bool { return eps[a].Port < eps[b].Port })
	return eps
}

// ClojureVersion reads the Clojure version from the classpath's
// clojure-X.Y.Z.jar, or "" when it is not there.
var clojureJarRe = regexp.MustCompile(`^clojure-(\d[^/]*)\.jar$`)

func ClojureVersion(cp []string) string {
	for _, e := range cp {
		if m := clojureJarRe.FindStringSubmatch(filepath.Base(e)); m != nil {
			return m[1]
		}
	}
	return ""
}

// String is handy in test failures.
func (e Endpoint) String() string { return fmt.Sprintf("%s :%s", e.Kind, e.Port) }

// ---- on-demand actions (Phase 2) ----

// jcmdOne runs a single jcmd command against p, checking ownership first and
// turning attach failures into readable errors.
func jcmdOne(ctx context.Context, p Process, d time.Duration, args ...string) (string, error) {
	if p.UID != os.Getuid() {
		return "", fmt.Errorf("this JVM belongs to %s; the JDK tools can only attach to your own JVMs", p.User)
	}
	out, err := run(ctx, d, jdkTool(p.Path, "jcmd"), append([]string{strconv.Itoa(p.PID)}, args...)...)
	if err != nil {
		return "", fmt.Errorf("%s", attachFailure(out, err))
	}
	return trimJcmdHeader(out), nil
}

// trimJcmdHeader drops the leading "1234:" line jcmd prints.
func trimJcmdHeader(out string) string {
	first, rest, ok := strings.Cut(out, "\n")
	if ok && strings.HasSuffix(strings.TrimSpace(first), ":") && !strings.Contains(strings.TrimSpace(first), " ") {
		out = rest
	}
	return strings.TrimRight(out, "\n ")
}

// ThreadDump returns the full jcmd Thread.print text (stacks, locks, and any
// deadlock report). The JVM reaches a safepoint briefly to produce it.
func ThreadDump(ctx context.Context, p Process) (string, error) {
	return jcmdOne(ctx, p, 15*time.Second, "Thread.print", "-l")
}

// ClassHistogram returns the top classes by retained bytes. jcmd's default
// forces a full GC first so only live objects are counted, which pauses the JVM.
func ClassHistogram(ctx context.Context, p Process, top int) (string, error) {
	out, err := jcmdOne(ctx, p, 40*time.Second, "GC.class_histogram")
	if err != nil {
		return "", err
	}
	return trimHistogram(out, top), nil
}

// trimHistogram keeps the header, the first top entries and the Total line.
func trimHistogram(out string, top int) string {
	var keep []string
	entries := 0
	for _, l := range strings.Split(out, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
		case strings.HasPrefix(t, "num") || strings.HasPrefix(t, "---"):
			keep = append(keep, l)
		case strings.HasPrefix(t, "Total"):
			keep = append(keep, l)
		default:
			entries++
			if entries <= top {
				keep = append(keep, l)
			}
		}
	}
	if entries > top {
		keep = append(keep[:len(keep)-1:len(keep)-1], fmt.Sprintf("   … %d more classes not shown", entries-top), keep[len(keep)-1])
	}
	return strings.Join(keep, "\n")
}

// ForceGC asks the JVM for a full garbage collection and reports heap use
// (KB) before and after.
func ForceGC(ctx context.Context, p Process) (before, after int64, err error) {
	used := func() (int64, error) {
		out, err := jcmdOne(ctx, p, 10*time.Second, "GC.heap_info")
		if err != nil {
			return 0, err
		}
		var j JVM
		j.parseHeap(out)
		return j.HeapUsedKB, nil
	}
	if before, err = used(); err != nil {
		return
	}
	if _, err = jcmdOne(ctx, p, 40*time.Second, "GC.run"); err != nil {
		return
	}
	after, err = used()
	return
}
