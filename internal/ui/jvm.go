package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rex-sheridan/pidwick/internal/proc"
)

// Actions on the JVM tab that ask the JVM to do more than answer questions.
// The thread dump and class histogram open a full-pane view of their output
// (Esc returns to the summary, y copies it); the GC asks for confirmation.

type jvmMode int

const (
	jvmSummary jvmMode = iota
	jvmThreads
	jvmHisto
)

const histogramTop = 40

// jvmPane is what the JVM tab is showing for one process.
type jvmPane struct {
	key  string
	mode jvmMode
	busy bool
	text string
	err  string
	at   time.Time
}

// jvmAsk is a pending confirmation for an action that pauses the JVM.
type jvmAsk struct {
	p      proc.Process
	action jvmMode // jvmHisto, or jvmSummary meaning "force GC"
}

type (
	jvmOutMsg struct {
		key  string
		mode jvmMode
		text string
		err  error
	}
	jvmGCMsg struct {
		key           string
		before, after int64
		err           error
	}
)

// jvmTarget returns the selected process when the JVM tab can act on it.
func (m *Model) jvmTarget() (proc.Process, bool) {
	if m.tab != tabJVM {
		return proc.Process{}, false
	}
	p, ok := m.selected()
	if !ok || !m.selectedIsJava() {
		return p, false
	}
	if m.isEnded(p) {
		return p, false
	}
	return p, true
}

// jvmKey handles the JVM tab's own keys. handled is false when the key
// should fall through to the normal bindings.
func (m Model) jvmKey(key string) (tea.Model, tea.Cmd, bool) {
	p, ok := m.jvmTarget()
	if !ok {
		return m, nil, false
	}
	k := p.Key()
	inView := m.pane.key == k && m.pane.mode != jvmSummary
	switch key {
	case "esc":
		if inView {
			m.pane = jvmPane{}
			m.showCurrent()
			return m, nil, true
		}
	case "d":
		if m.pane.busy {
			return m, m.setFlash("Still collecting…"), true
		}
		m.pane = jvmPane{key: k, mode: jvmThreads, busy: true}
		m.detail.GotoTop()
		m.showCurrent()
		return m, fetchThreadDump(p), true
	case "i":
		if m.pane.busy {
			return m, m.setFlash("Still collecting…"), true
		}
		m.ask = &jvmAsk{p: p, action: jvmHisto}
		return m, nil, true
	case "g":
		if m.pane.busy {
			return m, m.setFlash("Still collecting…"), true
		}
		m.ask = &jvmAsk{p: p, action: jvmSummary}
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) handleAskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.ask
	switch msg.String() {
	case "y", "enter":
		m.ask = nil
		if a.action == jvmHisto {
			m.pane = jvmPane{key: a.p.Key(), mode: jvmHisto, busy: true}
			m.detail.GotoTop()
			m.showCurrent()
			return m, fetchHistogram(a.p)
		}
		m.pane = jvmPane{}
		return m, tea.Batch(m.setFlash("Running a full GC…"), runGC(a.p))
	case "esc", "n", "q":
		m.ask = nil
	}
	return m, nil
}

func (a *jvmAsk) view(width int) string {
	w := min(max(width-8, 40), 72)
	inner := w - 4
	var b strings.Builder
	title, what := "Force a full garbage collection?", "Runs jcmd GC.run in "
	body := "Every application thread stops until the collection finishes. On a large heap that can be seconds. It frees memory the JVM would have reclaimed anyway, and can hide or reveal leaks, so it changes what you are measuring."
	if a.action == jvmHisto {
		title, what = "Collect a class histogram?", "Runs jcmd GC.class_histogram in "
		body = "To count only live objects the JVM first runs a full GC, then walks the heap while every application thread is stopped. On a large heap that can take several seconds."
	}
	b.WriteString(sWarn.Bold(true).Render(title) + "\n")
	b.WriteString(sDim.Render(ansi.Truncate(what+fmt.Sprintf("%s (PID %d)", a.p.Name, a.p.PID), inner, "…")) + "\n\n")
	b.WriteString(ansi.Wrap(sText.Render(body), inner, "") + "\n\n")
	b.WriteString(sBold.Render("y") + sDim.Render(" go ahead   ") + sBold.Render("esc") + sDim.Render(" cancel"))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cWarn).
		Padding(0, 1).Width(w).Render(b.String())
}

func fetchThreadDump(p proc.Process) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := proc.ThreadDump(ctx, p)
		return jvmOutMsg{p.Key(), jvmThreads, out, err}
	}
}

func fetchHistogram(p proc.Process) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		out, err := proc.ClassHistogram(ctx, p, histogramTop)
		return jvmOutMsg{p.Key(), jvmHisto, out, err}
	}
}

func runGC(p proc.Process) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		before, after, err := proc.ForceGC(ctx, p)
		return jvmGCMsg{p.Key(), before, after, err}
	}
}

// applyJVMOut stores a finished thread dump or histogram if it is still wanted.
func (m *Model) applyJVMOut(msg jvmOutMsg) {
	if m.pane.key != msg.key || m.pane.mode != msg.mode {
		return
	}
	m.pane.busy = false
	m.pane.at = time.Now()
	if msg.err != nil {
		m.pane.err = msg.err.Error()
		return
	}
	m.pane.text = msg.text
}

// renderJVMPane draws a thread dump or class histogram.
func renderJVMPane(in *proc.Info, pane jvmPane, w int) string {
	d := &detailBuilder{w: w}
	title, wait := "Thread dump", "Collecting a thread dump…"
	if pane.mode == jvmHisto {
		title = fmt.Sprintf("Class histogram (top %d by size, live objects)", histogramTop)
		wait = "Collecting the class histogram. The JVM runs a full GC first, so this can take a few seconds…"
	}
	d.section(title)
	switch {
	case pane.busy:
		d.para(sDim.Render(wait))
	case pane.err != "":
		d.para(sWarn.Render(pane.err))
		d.line(sDim.Render("esc goes back."))
	default:
		n := strings.Count(pane.text, "\n") + 1
		d.line(sDim.Render(fmt.Sprintf("%d lines, taken %s ago · esc back · y copy all · J/K scroll", n, humanDur(time.Since(pane.at)))))
		d.line("")
		if pane.mode == jvmThreads {
			d.b.WriteString(colorThreadDump(pane.text))
		} else {
			d.b.WriteString(pane.text)
		}
		d.line("")
	}
	return d.b.String()
}

// colorThreadDump highlights thread names, blocked/deadlocked threads and
// application frames so the interesting parts stand out in a long dump.
func colorThreadDump(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, `"`):
			lines[i] = sBold.Render(l)
		case strings.Contains(t, "Thread.State: BLOCKED"), strings.Contains(l, "deadlock"), strings.Contains(l, "waiting to lock"):
			lines[i] = sWarn.Render(l)
		case strings.HasPrefix(t, "at ") && !strings.HasPrefix(t, "at java.") && !strings.HasPrefix(t, "at jdk.") && !strings.HasPrefix(t, "at sun."):
			lines[i] = sText.Render(l)
		default:
			lines[i] = sDim.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}
