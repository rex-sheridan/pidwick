package proc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Kind is a cheap, path-based classification of where a process came from.
type Kind int

const (
	KindUnknown Kind = iota
	KindKernel
	KindSystem // Apple OS locations
	KindApp    // /Applications, /Library
	KindBrew   // Homebrew and /usr/local
	KindUser   // somewhere under the user's home directory
	KindTemp   // temp directories
)

func (k Kind) Tag() string {
	switch k {
	case KindKernel:
		return "krn"
	case KindSystem:
		return "sys"
	case KindApp:
		return "app"
	case KindBrew:
		return "brw"
	case KindUser:
		return "usr"
	case KindTemp:
		return "tmp"
	}
	return " · "
}

func (k Kind) String() string {
	switch k {
	case KindKernel:
		return "kernel"
	case KindSystem:
		return "macOS system location"
	case KindApp:
		return "installed application"
	case KindBrew:
		return "Homebrew / local install"
	case KindUser:
		return "user home directory"
	case KindTemp:
		return "temporary directory"
	}
	return "unknown location"
}

type Process struct {
	PID     int
	PPID    int
	UID     int
	User    string
	CPU     float64
	RSSKB   int64
	Age     time.Duration
	Path    string // executable path; empty for kernel_task
	Name    string
	Command string // full command line
	Kind    Kind
	Detail  string // what a generic runtime (java, node, python…) is running
}

func (p Process) Key() string { return fmt.Sprintf("%d:%s", p.PID, p.Path) }

var userCache = map[int]string{}

func userName(uid int) string {
	if n, ok := userCache[uid]; ok {
		return n
	}
	n := strconv.Itoa(uid)
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		n = u.Username
	}
	userCache[uid] = n
	return n
}

// List returns every process visible to the current user.
func List(ctx context.Context) ([]Process, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ps", "-axww", "-o", "pid=,ppid=,uid=,pcpu=,rss=,etime=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	cmds := map[int]string{}
	if cout, err := exec.CommandContext(ctx, "ps", "-axww", "-o", "pid=,command=").Output(); err == nil {
		for _, line := range strings.Split(string(cout), "\n") {
			f, rest := peel(line, 1)
			if len(f) == 1 {
				if pid, err := strconv.Atoi(f[0]); err == nil {
					cmds[pid] = strings.TrimSpace(rest)
				}
			}
		}
	}

	home, _ := os.UserHomeDir()
	var procs []Process
	for _, line := range bytes.Split(out, []byte("\n")) {
		f, comm := peel(string(line), 6)
		if len(f) < 6 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		uid, e3 := strconv.Atoi(f[2])
		cpu, _ := strconv.ParseFloat(f[3], 64)
		rss, _ := strconv.ParseInt(f[4], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		comm = strings.TrimSpace(comm)
		p := Process{
			PID: pid, PPID: ppid, UID: uid, User: userName(uid),
			CPU: cpu, RSSKB: rss, Age: parseEtime(f[5]),
			Command: cmds[pid],
		}
		if strings.HasPrefix(comm, "/") {
			p.Path = comm
			p.Name = filepath.Base(comm)
		} else {
			p.Name = comm
			if pid != 0 {
				// ps reports a bare name for processes that rewrite argv[0];
				// ask the kernel for the real executable path instead.
				if exe := execPath(pid); exe != "" {
					p.Path = exe
				}
			}
		}
		if strings.Contains(p.Name, "/") {
			p.Name = filepath.Base(p.Name) // e.g. "./pidwick"
		}
		if p.Command == "" {
			p.Command = comm
		}
		p.Kind = classify(p, home)
		p.Detail = runtimeDetail(p)
		procs = append(procs, p)
	}
	return procs, nil
}

// peel splits off the first n whitespace-separated tokens and returns the
// remainder untouched (so paths containing spaces survive).
func peel(s string, n int) ([]string, string) {
	var toks []string
	rest := strings.TrimLeft(s, " \t")
	for len(toks) < n && rest != "" {
		i := strings.IndexAny(rest, " \t")
		if i < 0 {
			toks = append(toks, rest)
			rest = ""
			break
		}
		toks = append(toks, rest[:i])
		rest = strings.TrimLeft(rest[i:], " \t")
	}
	return toks, rest
}

// parseEtime parses ps's [[dd-]hh:]mm:ss elapsed format.
func parseEtime(s string) time.Duration {
	var days int
	if i := strings.Index(s, "-"); i >= 0 {
		days, _ = strconv.Atoi(s[:i])
		s = s[i+1:]
	}
	parts := strings.Split(s, ":")
	secs := 0
	for _, p := range parts {
		v, _ := strconv.Atoi(p)
		secs = secs*60 + v
	}
	return time.Duration(days)*24*time.Hour + time.Duration(secs)*time.Second
}

var systemPrefixes = []string{
	"/System/", "/usr/libexec/", "/usr/sbin/", "/usr/bin/", "/sbin/", "/bin/",
	"/Library/Apple/", "/usr/standalone/", "/private/var/db/com.apple",
}

func classify(p Process, home string) Kind {
	if p.Path == "" {
		if p.PID == 0 {
			return KindKernel
		}
		return KindUnknown
	}
	path := p.Path
	for _, pre := range systemPrefixes {
		if strings.HasPrefix(path, pre) {
			return KindSystem
		}
	}
	switch {
	case strings.HasPrefix(path, "/tmp/"), strings.HasPrefix(path, "/private/tmp/"),
		strings.HasPrefix(path, "/var/tmp/"), strings.HasPrefix(path, "/private/var/tmp/"),
		strings.HasPrefix(path, "/private/var/folders/"), strings.HasPrefix(path, "/var/folders/"):
		return KindTemp
	case strings.HasPrefix(path, "/opt/homebrew/"), strings.HasPrefix(path, "/usr/local/"):
		return KindBrew
	case home != "" && strings.HasPrefix(path, home+"/"):
		return KindUser
	case strings.HasPrefix(path, "/Applications/"), strings.HasPrefix(path, "/Library/"):
		return KindApp
	}
	return KindUnknown
}

// execPath asks the kernel for a process's real executable path. It only
// works for processes the caller may inspect (typically their own).
func execPath(pid int) string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) <= 4 {
		return ""
	}
	raw = raw[4:] // skip argc
	if i := bytes.IndexByte(raw, 0); i > 0 {
		if s := string(raw[:i]); strings.HasPrefix(s, "/") {
			return s
		}
	}
	return ""
}

// runtimeDetail names what a generic runtime process is actually running, so
// a dozen "java" rows are distinguishable in the list.
func runtimeDetail(p Process) string {
	exe := strings.ToLower(filepath.Base(p.Path))
	if !(strings.HasPrefix(exe, "java") || strings.HasPrefix(exe, "node") || strings.HasPrefix(exe, "python") ||
		strings.HasPrefix(exe, "ruby") || exe == "deno" || exe == "bun" || strings.HasPrefix(exe, "php")) {
		return ""
	}
	l := ParseLaunch(readArgv(p.PID, p.Command))
	switch l.MainKind {
	case "jar", "script":
		return filepath.Base(l.Main)
	case "inline", "":
		return ""
	}
	return strings.TrimPrefix(l.Main, "clojure.main -m ")
}
