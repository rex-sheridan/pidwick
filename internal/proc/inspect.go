package proc

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Info is everything Pidwick can tell you about one process.
type Info struct {
	Proc     Process
	Lineage  []Process // root ... parent
	Children []Process

	Exists   bool
	Size     int64
	Modified time.Time
	Created  time.Time
	Role     string // e.g. "XPC service bundled with Foo.app"
	Bundle   *Bundle
	Sign     Signature
	Package  *Package
	Brew     string // Homebrew formula
	Job      *Job
	JobLabel string // label from launchctl when no plist matched
	KB       *Entry
	Handles  Handles
	Verdict  Verdict
}

type Package struct {
	ID        string
	Version   string
	Installed time.Time
}

// static facts about a binary are cached by path
type static struct {
	exists   bool
	size     int64
	modified time.Time
	created  time.Time
	role     string
	bundle   *Bundle
	sign     Signature
	pkg      *Package
	brew     string
}

var staticCache sync.Map // path -> *static

func staticFor(ctx context.Context, path string) *static {
	if v, ok := staticCache.Load(path); ok {
		return v.(*static)
	}
	s := &static{}
	if fi, err := os.Stat(path); err == nil {
		s.exists = true
		s.size = fi.Size()
		s.modified = fi.ModTime()
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			s.created = time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
		}
	}
	s.bundle = readBundle(path)
	target := path
	if s.bundle != nil {
		target = s.bundle.Outermost
	}
	if s.exists {
		s.sign = inspectSignature(ctx, path, target)
		s.pkg = packageFor(ctx, path)
	}
	s.brew = brewFormula(path)
	s.role = roleOf(path, s.bundle)
	staticCache.Store(path, s)
	return s
}

func roleOf(path string, b *Bundle) string {
	appName := ""
	if b != nil && b.Outermost != "" {
		appName = strings.TrimSuffix(filepath.Base(b.Outermost), ".app")
	}
	switch {
	case strings.Contains(path, ".xpc/"):
		if appName != "" {
			return "XPC service bundled inside " + appName + " (a sandboxed helper the app delegates work to)"
		}
		return "XPC service (a helper process that another program delegates work to)"
	case strings.Contains(path, "/Contents/Library/LoginItems/"):
		return "login item helper bundled inside " + appName + " (starts at login to run in the background)"
	case strings.Contains(path, "/Library/SystemExtensions/"):
		return "system extension (a driver-like component approved in System Settings)"
	case strings.Contains(path, "/Library/PrivilegedHelperTools/"):
		return "privileged helper tool (a root helper installed by an app that needed admin rights)"
	case strings.Contains(path, "/Library/Frameworks/") || strings.Contains(path, "/Contents/Frameworks/"):
		if appName != "" {
			return "helper executable inside " + appName + "'s frameworks"
		}
	case b != nil && b.Path == b.Outermost:
		return "main executable of the application " + appName
	case appName != "":
		return "executable inside the application " + appName
	}
	return ""
}

var brewRe = regexp.MustCompile(`/(?:opt/homebrew|usr/local)/(?:Cellar|Caskroom)/([^/]+)/`)

func brewFormula(path string) string {
	if m := brewRe.FindStringSubmatch(path); m != nil {
		return m[1]
	}
	return ""
}

func packageFor(ctx context.Context, path string) *Package {
	out, err := run(ctx, 4*time.Second, "pkgutil", "--file-info", path)
	if err != nil {
		return nil
	}
	var id string
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "pkgid:"); ok {
			id = strings.TrimSpace(v)
			break
		}
	}
	if id == "" {
		return nil
	}
	p := &Package{ID: id}
	if info, err := run(ctx, 4*time.Second, "pkgutil", "--pkg-info", id); err == nil {
		for _, l := range strings.Split(info, "\n") {
			if v, ok := strings.CutPrefix(l, "version:"); ok {
				p.Version = strings.TrimSpace(v)
			}
			if v, ok := strings.CutPrefix(l, "install-time:"); ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
					p.Installed = time.Unix(n, 0)
				}
			}
		}
	}
	return p
}

func launchctlLabel(ctx context.Context, pid int) string {
	out, err := run(ctx, 3*time.Second, "launchctl", "list")
	if err != nil {
		return ""
	}
	want := strconv.Itoa(pid)
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) == 3 && f[0] == want {
			return f[2]
		}
	}
	return ""
}

// Inspect builds a full report for p. all is the current process table, used
// to work out lineage and children.
func Inspect(ctx context.Context, p Process, all []Process) *Info {
	info := &Info{Proc: p}
	byPID := map[int]Process{}
	for _, q := range all {
		byPID[q.PID] = q
		if q.PPID == p.PID && q.PID != p.PID {
			info.Children = append(info.Children, q)
		}
	}
	seen := map[int]bool{p.PID: true}
	for cur := p; cur.PPID > 0 || cur.PID == 0; {
		par, ok := byPID[cur.PPID]
		if !ok || seen[par.PID] {
			break
		}
		seen[par.PID] = true
		info.Lineage = append([]Process{par}, info.Lineage...)
		cur = par
	}

	if e, ok := Lookup(p.Name); ok {
		info.KB = &e
	}

	var wg sync.WaitGroup
	if p.Path != "" {
		s := staticFor(ctx, p.Path)
		info.Exists, info.Size, info.Modified, info.Created = s.exists, s.size, s.modified, s.created
		info.Role, info.Bundle, info.Sign, info.Package, info.Brew = s.role, s.bundle, s.sign, s.pkg, s.brew
		info.Job = JobForExecutable(p.Path)
	}
	if info.Job == nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if label := launchctlLabel(ctx, p.PID); label != "" {
				info.Job = JobForLabel(label)
				if info.Job == nil {
					info.JobLabel = label
				}
			}
		}()
	}
	info.Handles = inspectHandles(ctx, p.PID, true)
	wg.Wait()

	info.Verdict = Judge(info)
	return info
}
