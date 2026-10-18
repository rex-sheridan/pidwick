package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rex/pidwick/internal/proc"
)

type detailBuilder struct {
	w int
	b strings.Builder
}

func (d *detailBuilder) section(title string) {
	if d.b.Len() > 0 {
		d.b.WriteString("\n")
	}
	d.b.WriteString(sSection.Render(strings.ToUpper(title)) + "\n")
}

func (d *detailBuilder) para(s string) {
	d.b.WriteString(wrap(s, d.w, "") + "\n")
}

func (d *detailBuilder) bullet(glyph, s string) {
	lines := strings.Split(wrap(s, d.w-2, ""), "\n")
	for i, l := range lines {
		if i == 0 {
			d.b.WriteString(glyph + " " + l + "\n")
		} else {
			d.b.WriteString("  " + l + "\n")
		}
	}
}

func (d *detailBuilder) kv(k, v string) {
	if v == "" {
		return
	}
	const kw = 11
	lines := strings.Split(wrap(v, d.w-kw-1, ""), "\n")
	for i, l := range lines {
		label := strings.Repeat(" ", kw)
		if i == 0 {
			label = sDim.Render(fmt.Sprintf("%-*s", kw, k))
		}
		d.b.WriteString(label + " " + l + "\n")
	}
}

func (d *detailBuilder) line(s string) { d.b.WriteString(s + "\n") }

func wrap(s string, w int, _ string) string {
	if w < 10 {
		w = 10
	}
	return strings.TrimRight(ansi.Wrap(sText.Render(s), w, "/ "), "\n")
}

func truncLeft(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && ansi.StringWidth("…"+string(r)) > w {
		r = r[1:]
	}
	return "…" + string(r)
}

func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f GB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.0f KB", float64(n)/k)
	}
	return fmt.Sprintf("%d B", n)
}

func humanDur(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04") + "  (" + humanDur(time.Since(t)) + " ago)"
}

func procLabel(p proc.Process) string {
	return fmt.Sprintf("%s (%d)", p.Name, p.PID)
}

func renderOverview(in *proc.Info, w int) string {
	d := &detailBuilder{w: w}
	p := in.Proc
	v := in.Verdict

	d.line(lipgloss.NewStyle().Foreground(levelColor(v.Level)).Bold(true).Render(v.Headline))

	// WHAT IT IS
	d.section("What it is")
	switch {
	case in.KB != nil:
		d.para(in.KB.Summary)
		if in.KB.Inferred {
			d.line(sDim.Render("Inferred from its name and the framework it lives in; Apple doesn't document this one."))
		}
	case in.Sign.Kind == proc.SignerApple:
		d.para(fmt.Sprintf("“%s” is an Apple-signed part of macOS that Pidwick has no description for yet.%s", p.Name, frameworkHint(p.Path)))
	case in.Bundle != nil && in.Bundle.Name != "":
		msg := fmt.Sprintf("Not in Pidwick's built-in list of known processes. It belongs to the application %s%s.",
			in.Bundle.Name, versionSuffix(in.Bundle.Version))
		if in.Role != "" {
			msg += " Specifically, it's the " + in.Role + "."
		}
		d.para(msg)
	default:
		msg := fmt.Sprintf("“%s” isn't in Pidwick's built-in list of known processes.", p.Name)
		if in.Role != "" {
			msg += " It looks like a " + in.Role + "."
		}
		d.para(msg)
	}

	// WHAT IT'S RUNNING (interpreted command line)
	if l := in.Launch; l.Main != "" || l.Runtime != "" {
		d.section("What it's running")
		d.kv("Runtime", l.Runtime)
		if l.Main != "" {
			d.kv(strings.ToUpper(l.MainKind[:1])+l.MainKind[1:], sBold.Render(l.Main))
		}
		for _, h := range l.Hints {
			d.para(h)
		}
		if len(l.JarInfo) > 0 {
			ji := l.JarInfo
			d.kv("Jar says", strings.TrimSpace(joinNonEmpty(" ", ji["Implementation-Title"], ji["Implementation-Version"], dashed(ji["Implementation-Vendor"]))))
			d.kv("Main-Class", ji["Main-Class"])
		}
		if len(l.AppArgs) > 0 {
			d.kv("Arguments", strings.Join(l.AppArgs, " "))
		}
		d.kv("Started in", tilde(in.Cwd))
		if len(l.Options) > 0 {
			d.kv("Options", strings.Join(l.Options, " "))
		}
		if len(l.Props) > 0 {
			var ps []string
			for _, p := range l.Props {
				if p[1] == "" {
					ps = append(ps, "-D"+p[0])
				} else {
					ps = append(ps, "-D"+p[0]+"="+tilde(p[1]))
				}
			}
			d.kv("Properties", strings.Join(ps, "  "))
		}
		if n := len(l.Classpath); n > 0 {
			d.kv("Classpath", describeClasspath(l.Classpath))
		}
	}

	// WHY IT'S HERE
	d.section("Why it's on your machine")
	if in.KB != nil && in.KB.Why != "" {
		d.para(in.KB.Why)
	}
	for _, s := range whyBullets(in) {
		d.bullet(sDim.Render("•"), s)
	}

	// SAFE TO QUIT
	d.section("Safe to quit?")
	d.para(quitAdvice(in))

	// WHO MAKES IT
	d.section("Who makes it")
	switch {
	case in.Sign.Kind == proc.SignerApple:
		d.kv("Vendor", "Apple Inc.")
	case in.Sign.Signer != "":
		d.kv("Vendor", in.Sign.Signer)
	case in.Bundle != nil && in.Bundle.Copyright != "":
		d.kv("Vendor", in.Bundle.Copyright+"  (self-declared, unverified)")
	default:
		d.kv("Vendor", sWarn.Render("unknown: no code-signing identity"))
	}
	d.kv("Signed by", in.Sign.Kind.String())
	d.kv("Team ID", in.Sign.TeamID)
	if in.Bundle != nil {
		d.kv("App", in.Bundle.Name+versionSuffix(in.Bundle.Version))
		d.kv("Bundle ID", in.Bundle.Identifier)
		if in.Bundle.Copyright != "" && in.Sign.Signer != "" {
			d.kv("Copyright", in.Bundle.Copyright)
		}
	} else if in.Sign.Identifier != "" {
		d.kv("Identifier", in.Sign.Identifier)
	}
	if in.Sign.Gatekeeper != "" {
		d.kv("Gatekeeper", in.Sign.Gatekeeper)
	}

	// WHERE
	d.section("Where it lives")
	if p.Path == "" {
		d.para("Built into the kernel; there is no file on disk.")
	} else {
		d.kv("Path", p.Path)
		if in.Launch.Main == "" {
			d.kv("Started in", tilde(in.Cwd))
		}
		d.kv("Location", p.Kind.String())
		d.kv("Role", in.Role)
		if in.Exists {
			d.kv("Size", humanBytes(in.Size))
			d.kv("Created", ago(in.Created))
			d.kv("Modified", ago(in.Modified))
		} else {
			d.kv("File", sBad.Render("missing from disk"))
		}
		if in.Package != nil {
			s := in.Package.ID
			if in.Package.Version != "" {
				s += " " + in.Package.Version
			}
			if !in.Package.Installed.IsZero() {
				s += ", installed " + ago(in.Package.Installed)
			}
			d.kv("Installer", s)
		}
		if in.Brew != "" {
			d.kv("Homebrew", in.Brew+"  (brew info "+in.Brew+")")
		}
	}

	// HOW IT STARTS
	d.section("How it starts")
	if in.Job != nil {
		d.kv("Job", in.Job.Label)
		d.kv("Plist", in.Job.PlistPath)
		d.kv("Kind", in.Job.Domain)
		var when []string
		if in.Job.RunAtLoad {
			when = append(when, "automatically at boot/login")
		}
		when = append(when, in.Job.OnDemand...)
		if len(when) > 0 {
			d.kv("Starts", strings.Join(when, "; "))
		}
		if in.Job.KeepAlive {
			d.kv("Restarts", "launchd relaunches it if it exits")
		}
	} else if in.JobLabel != "" {
		d.kv("Job label", in.JobLabel)
		d.para("Started by launchd without a plist we could find (typical for apps you opened yourself).")
	} else {
		d.para("No launchd job definition found for this executable; it was most likely started by its parent process.")
	}
	d.kv("Parent", parentLabel(in))

	// TRUST CHECK
	d.section("Trust check")
	for _, f := range v.Findings {
		d.bullet(markGlyph(f.Mark), f.Text)
	}
	d.line(sDim.Render("Heuristics, not an antivirus. Verify before acting."))

	// COMMAND LINE
	d.section("Command line")
	cmd := p.Command
	if len(cmd) > 8000 {
		cmd = cmd[:8000] + "…"
	}
	d.para(cmd)

	// FAMILY
	d.section("Family tree")
	for i, a := range in.Lineage {
		d.line(strings.Repeat("  ", i) + sDim.Render(procLabel(a)))
	}
	d.line(strings.Repeat("  ", len(in.Lineage)) + sAccent.Render("▸ "+procLabel(p)))
	kids := in.Children
	sort.Slice(kids, func(i, j int) bool { return kids[i].PID < kids[j].PID })
	for i, c := range kids {
		if i == 12 {
			d.line(strings.Repeat("  ", len(in.Lineage)+1) + sDim.Render(fmt.Sprintf("… and %d more children", len(kids)-12)))
			break
		}
		d.line(strings.Repeat("  ", len(in.Lineage)+1) + sDim.Render("└ "+procLabel(c)))
	}
	return d.b.String()
}

var frameworkRe = regexp.MustCompile(`/([A-Za-z0-9]+)\.framework/`)

// frameworkHint gives a clue about an undocumented system process from the
// framework that ships it.
func frameworkHint(path string) string {
	if m := frameworkRe.FindStringSubmatch(path); m != nil {
		return " It ships inside Apple's " + m[1] + " framework, so it most likely supports that area of the system."
	}
	return ""
}

func tilde(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+"/") {
			return "~" + path[len(home):]
		}
	}
	return path
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func dashed(s string) string {
	if s == "" {
		return ""
	}
	return "by " + s
}

// describeClasspath summarises a long Java classpath: entry count, the
// directories (usually the project's own source) and a few jar names.
func describeClasspath(cp []string) string {
	var dirs, jars []string
	for _, e := range cp {
		if strings.HasSuffix(e, ".jar") || strings.HasSuffix(e, ".zip") {
			jars = append(jars, filepath.Base(e))
		} else if e != "" {
			dirs = append(dirs, tilde(e))
		}
	}
	var parts []string
	if len(dirs) > 0 {
		parts = append(parts, "dirs: "+strings.Join(dirs[:min(len(dirs), 4)], ", "))
		if len(dirs) > 4 {
			parts[len(parts)-1] += fmt.Sprintf(" (+%d)", len(dirs)-4)
		}
	}
	if len(jars) > 0 {
		shown := jars[:min(len(jars), 4)]
		s := "jars: " + strings.Join(shown, ", ")
		if len(jars) > len(shown) {
			s += fmt.Sprintf(" (+%d more)", len(jars)-len(shown))
		}
		parts = append(parts, s)
	}
	return fmt.Sprintf("%d entries. %s. Full list under Command line below.", len(cp), strings.Join(parts, "; "))
}

func versionSuffix(v string) string {
	if v == "" {
		return ""
	}
	return " " + v
}

func parentLabel(in *proc.Info) string {
	if len(in.Lineage) == 0 {
		return ""
	}
	par := in.Lineage[len(in.Lineage)-1]
	s := procLabel(par)
	if par.PID == 1 {
		s += ": macOS's service manager"
	}
	return s
}

func whyBullets(in *proc.Info) []string {
	var out []string
	p := in.Proc
	if len(in.Lineage) > 0 {
		par := in.Lineage[len(in.Lineage)-1]
		switch {
		case par.PID == 1 && in.Job == nil && in.JobLabel == "" && p.Kind != proc.KindSystem:
			out = append(out, "Its parent process has exited, so macOS's launchd adopted it. That's normal for something started from a terminal that was later closed, or for programs that deliberately detach (servers, build daemons). Check 'Started in' for where it was launched from.")
		case par.PID == 1:
			out = append(out, "Launched directly by launchd, the usual route for background services, login items and apps you open from Finder or the Dock.")
		default:
			out = append(out, fmt.Sprintf("Started by %s, so something in that program (or whoever launched it) asked for it to run.", procLabel(par)))
		}
	}
	if in.Job != nil {
		src := "an installed launch job"
		switch {
		case in.Job.AppleSigned:
			src = "one of macOS's own launch jobs"
		case strings.Contains(in.Job.PlistPath, "/Library/LaunchDaemons"):
			src = "a system-wide launch daemon added by installed software"
		case strings.Contains(in.Job.PlistPath, "LaunchAgents"):
			src = "a launch agent added by installed software"
		}
		out = append(out, "Registered with launchd as "+in.Job.Label+" by "+src+".")
	}
	if in.Bundle != nil && in.Bundle.Name != "" {
		out = append(out, "Belongs to "+in.Bundle.Name+". If you don't recognise that app, check "+filepath.Dir(in.Bundle.Outermost)+".")
	}
	if in.Package != nil {
		out = append(out, "Its file was installed by package "+in.Package.ID+".")
	}
	if in.Brew != "" {
		out = append(out, "Installed by Homebrew (package: "+in.Brew+"), either by you or as a dependency of something you installed.")
	}
	if p.Kind == proc.KindUser && in.Bundle == nil {
		out = append(out, "Runs from your home folder, so a tool or script you (or an installer) put there is responsible.")
	}
	if p.User != "" && p.UID == 0 && p.Kind != proc.KindSystem {
		out = append(out, "Runs as root (full system privileges), worth a closer look since it's not an Apple component.")
	}
	return out
}

func quitAdvice(in *proc.Info) string {
	if in.KB != nil && in.KB.Quit != "" {
		return in.KB.Quit
	}
	p := in.Proc
	switch {
	case in.Verdict.Level == proc.LevelSuspicious:
		return "Don't just kill it: first note the path and who launched it, then quit it and remove the file and any launch job that restarts it (see 'How it starts')."
	case in.Launch.Main != "" && in.KB == nil:
		return fmt.Sprintf("Depends on what it runs (%s). If you started it from a terminal, press Ctrl-C there; otherwise check who owns it before killing.", in.Launch.Main)
	case in.Job != nil && in.Job.KeepAlive:
		return "launchd restarts it automatically, so quitting it won't stick. To stop it for good, uninstall the software or unload its launch job."
	case p.Kind == proc.KindSystem:
		return "It's part of macOS. Killing system processes can cause glitches or a logout, and macOS usually restarts them anyway. Leave it unless you're troubleshooting."
	case in.Bundle != nil && in.Bundle.Name != "":
		return "Quit " + in.Bundle.Name + " normally from its menu (save your work first). Force-quitting can lose unsaved changes."
	}
	return "Not in the built-in knowledge base. Search for the name (press w) before force-quitting a background process; quitting an ordinary app you recognise is safe."
}

func renderFiles(in *proc.Info, w int) string {
	d := &detailBuilder{w: w}
	h := in.Handles
	if h.Denied {
		d.para(sWarn.Render("macOS didn't let Pidwick list this process's open files.") +
			" Processes owned by other users (usually root) need elevated rights. Run `sudo pidwick` to see them.")
		return d.b.String()
	}
	d.section("Working directory & executable")
	d.kv("cwd", h.Cwd)
	d.kv("Binary", in.Proc.Path)

	d.section(fmt.Sprintf("Open files (%d)", len(h.Files)))
	if len(h.Files) == 0 {
		d.line(sDim.Render("none"))
	}
	for i, f := range h.Files {
		if i == 150 {
			d.line(sDim.Render(fmt.Sprintf("… %d more", len(h.Files)-150)))
			break
		}
		d.line(fmt.Sprintf("%s %s %s", sDim.Render(fmt.Sprintf("%-5s", f.FD)), sDim.Render(fmt.Sprintf("%-4s", f.Type)), truncLeft(f.Name, w-12)))
	}

	d.section("Other handles")
	d.line(fmt.Sprintf("%s pipes · %s unix sockets · %s devices",
		sBold.Render(fmt.Sprint(h.Pipes)), sBold.Render(fmt.Sprint(h.Sockets)), sBold.Render(fmt.Sprint(len(h.Devices)))))
	for _, dv := range h.Devices {
		d.line(sDim.Render(fmt.Sprintf("%-5s ", dv.FD)) + truncLeft(dv.Name, w-6))
	}

	var third, sys []string
	for _, l := range h.Libs {
		if isSystemLib(l) {
			sys = append(sys, l)
		} else {
			third = append(third, l)
		}
	}
	d.section(fmt.Sprintf("Code loaded (%d system, %d other)", len(sys), len(third)))
	if len(third) == 0 {
		d.line(sDim.Render("Only Apple/system libraries."))
	}
	for i, l := range third {
		if i == 60 {
			d.line(sDim.Render(fmt.Sprintf("… %d more", len(third)-60)))
			break
		}
		d.line(truncLeft(l, w))
	}
	return d.b.String()
}

func isSystemLib(p string) bool {
	for _, pre := range []string{"/System/", "/usr/lib/", "/Library/Apple/", "/private/var/db/dyld", "/usr/libexec/", "/System/Volumes/Preboot/Cryptexes/"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func renderNetwork(in *proc.Info, w int) string {
	d := &detailBuilder{w: w}
	h := in.Handles
	if h.Denied {
		d.para(sWarn.Render("macOS didn't let Pidwick inspect this process's connections.") +
			" Run `sudo pidwick` to see root-owned processes.")
		return d.b.String()
	}
	var listen, other []proc.Conn
	for _, c := range h.Conns {
		if c.State == "LISTEN" || (c.Proto == "UDP" && c.Remote == "") {
			listen = append(listen, c)
		} else {
			other = append(other, c)
		}
	}
	d.section(fmt.Sprintf("Listening / bound ports (%d)", len(listen)))
	if len(listen) == 0 {
		d.line(sDim.Render("Not accepting connections."))
	}
	for _, c := range listen {
		exposure := sGood.Render("localhost only")
		switch c.Scope {
		case "any":
			exposure = sWarn.Render("all interfaces")
		case "LAN":
			exposure = sWarn.Render("local network")
		}
		d.line(fmt.Sprintf("%s %s  %s", sBold.Render(c.Proto), truncLeft(c.Local, w-24), exposure))
	}

	d.section(fmt.Sprintf("Connections (%d)", len(other)))
	if len(other) == 0 {
		d.line(sDim.Render("No active connections."))
	}
	for _, c := range other {
		st := c.State
		if st == "" {
			st = c.Proto
		}
		scope := sDim.Render(c.Scope)
		if c.Scope == "public" {
			scope = sAccent.Render("internet")
		}
		host := ""
		if c.Host != "" {
			host = "  " + sText.Render(c.Host)
		}
		line := fmt.Sprintf("%s %s → %s", sDim.Render(fmt.Sprintf("%-11s", st)), c.Local, c.Remote)
		d.line(ansi.Truncate(line, w, "…"))
		d.line("    " + scope + host)
	}
	if len(other) > 0 {
		d.line("")
		d.line(sDim.Render("Host names come from reverse DNS and may be blank or generic (CDN/cloud)."))
	}
	return d.b.String()
}

var tabNames = []string{"1 Overview", "2 Files", "3 Network"}

func tabBar(active int, w int) string {
	names := tabNames
	var parts []string
	for i, n := range names {
		if i == active {
			parts = append(parts, lipgloss.NewStyle().Bold(true).Foreground(cAccent).Underline(true).Render(n))
		} else {
			parts = append(parts, sDim.Render(n))
		}
	}
	sep := "  │  "
	if w < 42 {
		sep = " │ "
	}
	return ansi.Truncate(strings.Join(parts, sDim.Render(sep)), w, "…")
}
