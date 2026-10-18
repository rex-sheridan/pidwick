package ui

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rex/pidwick/internal/proc"
)

type killPrompt struct {
	p        proc.Process
	kids     []proc.Process // all descendants
	warnings []string
	blocked  string
}

type killedMsg struct {
	text string
	err  error
}

func descendants(all []proc.Process, pid int) []proc.Process {
	children := map[int][]proc.Process{}
	for _, p := range all {
		if p.PID != p.PPID {
			children[p.PPID] = append(children[p.PPID], p)
		}
	}
	var out []proc.Process
	seen := map[int]bool{pid: true}
	queue := []int{pid}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if !seen[c.PID] {
				seen[c.PID] = true
				out = append(out, c)
				queue = append(queue, c.PID)
			}
		}
	}
	return out
}

func newKillPrompt(p proc.Process, all []proc.Process, in *proc.Info) *killPrompt {
	k := &killPrompt{p: p, kids: descendants(all, p.PID)}
	switch {
	case p.PID <= 1:
		k.blocked = "Killing " + p.Name + " would crash or reboot macOS. Pidwick won't do it."
	case p.PID == os.Getpid():
		k.blocked = "That's Pidwick itself. Press q to quit instead."
	}
	if p.UID != os.Getuid() {
		k.warnings = append(k.warnings, fmt.Sprintf("Owned by %s, not you: macOS will refuse unless Pidwick runs with sudo.", p.User))
	}
	if in != nil {
		if in.Verdict.Level == proc.LevelSystem || p.Kind == proc.KindSystem {
			k.warnings = append(k.warnings, "This is a macOS system component. Killing it can freeze the desktop, drop you to the login screen or lose unsaved work.")
		}
		if in.Job != nil && in.Job.KeepAlive {
			k.warnings = append(k.warnings, "launchd restarts this automatically, so it will likely come straight back.")
		}
		if in.Verdict.Level == proc.LevelSuspicious {
			k.warnings = append(k.warnings, "Flagged as suspicious. Killing it won't remove the file or any launch job that restarts it.")
		}
	}
	switch p.Name {
	case "WindowServer", "loginwindow":
		k.warnings = append(k.warnings, "Killing "+p.Name+" logs you out immediately.")
	}
	if len(k.kids) > 0 {
		k.warnings = append(k.warnings, fmt.Sprintf("It has %d child process(es); they keep running unless you terminate the whole tree.", len(k.kids)))
	}
	return k
}

func (k *killPrompt) view(width int) string {
	w := min(max(width-8, 40), 72)
	inner := w - 4
	var b strings.Builder
	b.WriteString(sBad.Bold(true).Render(fmt.Sprintf("Terminate %s (PID %d)?", k.p.Name, k.p.PID)) + "\n")
	b.WriteString(sDim.Render(ansi.Truncate(k.p.Path, inner, "…")) + "\n\n")
	if k.blocked != "" {
		b.WriteString(ansi.Wrap(sWarn.Render(k.blocked), inner, "") + "\n\n")
		b.WriteString(sDim.Render("esc  close"))
	} else {
		for _, wmsg := range k.warnings {
			b.WriteString(ansi.Wrap(sWarn.Render("! ")+sText.Render(wmsg), inner, "") + "\n")
		}
		if len(k.warnings) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(sBold.Render("t") + sDim.Render(" terminate (SIGTERM, lets it clean up)") + "\n")
		b.WriteString(sBold.Render("k") + sDim.Render(" force kill (SIGKILL, immediate)") + "\n")
		if len(k.kids) > 0 {
			b.WriteString(sBold.Render("a") + sDim.Render(fmt.Sprintf(" terminate it and its %d descendants", len(k.kids))) + "\n")
		}
		b.WriteString(sBold.Render("esc") + sDim.Render(" cancel"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBad).
		Padding(0, 1).Width(w).Render(b.String())
}

// send delivers sig to the targets, children first so parents can't respawn them.
func sendSignal(sig syscall.Signal, targets []proc.Process) killedMsg {
	name := "SIGTERM"
	if sig == syscall.SIGKILL {
		name = "SIGKILL"
	}
	var done int
	var firstErr error
	for _, t := range targets {
		err := syscall.Kill(t.PID, sig)
		if err == nil || err == syscall.ESRCH {
			done++
			continue
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("%s (%d): %w", t.Name, t.PID, err)
		}
	}
	if firstErr != nil {
		msg := "Couldn't signal " + firstErr.Error()
		if strings.Contains(firstErr.Error(), "operation not permitted") {
			msg = "Permission denied for " + targets[0].Name + ": it belongs to another user. Run `sudo pidwick`."
		}
		return killedMsg{text: msg, err: firstErr}
	}
	if len(targets) == 1 {
		return killedMsg{text: fmt.Sprintf("Sent %s to %s (%d)", name, targets[0].Name, targets[0].PID)}
	}
	return killedMsg{text: fmt.Sprintf("Sent %s to %d processes", name, done)}
}

// overlay draws box centered on top of base (both ANSI strings).
func overlay(base, box string, width, height int) string {
	lines := strings.Split(base, "\n")
	bl := strings.Split(box, "\n")
	bw := 0
	for _, l := range bl {
		bw = max(bw, ansi.StringWidth(l))
	}
	x0 := max(0, (width-bw)/2)
	y0 := max(0, (height-len(bl))/2)
	for i, l := range bl {
		y := y0 + i
		if y >= len(lines) {
			break
		}
		row := lines[y]
		pad := bw - ansi.StringWidth(l)
		lines[y] = ansi.Truncate(row+strings.Repeat(" ", max(0, x0-ansi.StringWidth(row))), x0, "") +
			l + strings.Repeat(" ", max(0, pad)) + ansi.Cut(row, x0+bw, width)
	}
	return strings.Join(lines, "\n")
}
