package ui

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rex-sheridan/pidwick/internal/proc"
)

func click(m tea.Model, x, y int) tea.Model {
	m, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	return m
}

func release(m tea.Model, x, y int) tea.Model {
	m, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone})
	return m
}

func drag(m tea.Model, x, y int) tea.Model {
	m, _ = m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	return m
}

func withInfo(t *testing.T, w, h int) tea.Model {
	t.Helper()
	m := loaded(t, w, h)
	p, _ := m.selected()
	var tm tea.Model = m
	tm, _ = tm.Update(inspectMsg{p.Key(), proc.Inspect(context.Background(), p, m.all)})
	return tm
}

func TestClickSelectsProcess(t *testing.T) {
	var tm tea.Model = loaded(t, 120, 40)
	m := tm.(Model)
	want := m.view[4]
	tm = click(tm, 5, listTop+4)
	got, _ := tm.(Model).selected()
	if got.PID != want.PID {
		t.Fatalf("clicked row 4 (pid %d) but selected pid %d", want.PID, got.PID)
	}
}

func TestClickTab(t *testing.T) {
	var tm tea.Model = loaded(t, 140, 40)
	m := tm.(Model)
	x := m.detailX()
	tm = click(tm, x+len("1 Overview")+8, tabRow) // inside "2 Files"
	if tm.(Model).tab != tabFiles {
		t.Fatalf("tab = %d, want files", tm.(Model).tab)
	}
	tm = click(tm, x+2, tabRow)
	if tm.(Model).tab != tabOverview {
		t.Fatalf("tab = %d, want overview", tm.(Model).tab)
	}
}

func TestDragSelectsAndCopies(t *testing.T) {
	var copied string
	old := writeClipboard
	writeClipboard = func(s string) error { copied = s; return nil }
	defer func() { writeClipboard = old }()

	tm := withInfo(t, 140, 50)
	m := tm.(Model)
	x := m.detailX()
	// find a line with the path text
	row := -1
	for i, l := range m.plain {
		if strings.HasPrefix(l, "Path ") {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("no Path row in overview")
	}
	y := detailTop + row - m.detail.YOffset
	tm = click(tm, x+12, y) // start of the value column
	tm = drag(tm, x+20, y)
	if !strings.Contains(ansi.Strip(tm.View()), "Path") {
		t.Fatal("view lost content while selecting")
	}
	tm = release(tm, x+20, y)
	want := strings.TrimSpace(m.plain[row][12:21])
	if copied != want {
		t.Fatalf("copied %q, want %q", copied, want)
	}
	if tm.(Model).flash == "" {
		t.Error("no flash after copy")
	}
}

func TestDoubleClickSelectsWord(t *testing.T) {
	var copied string
	old := writeClipboard
	writeClipboard = func(s string) error { copied = s; return nil }
	defer func() { writeClipboard = old }()

	tm := withInfo(t, 140, 50)
	m := tm.(Model)
	row := -1
	for i, l := range m.plain {
		if strings.HasPrefix(l, "Path ") {
			row = i
		}
	}
	y := detailTop + row - m.detail.YOffset
	x := m.detailX() + 14
	for i := 0; i < 2; i++ {
		tm = click(tm, x, y)
		tm = release(tm, x, y)
	}
	path := m.plain[row][12:]
	if !strings.HasPrefix(path, "/") || copied != strings.TrimSpace(path) {
		t.Fatalf("double-click copied %q, want path %q", copied, path)
	}
}

func TestSelectionTextAcrossLines(t *testing.T) {
	plain := []string{"hello world", "second line", "third"}
	s := selection{on: true, anchor: pos{0, 6}, head: pos{2, 2}}
	if got, want := s.text(plain), "world\nsecond line\nthi"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// reversed drag selects the same text
	r := selection{on: true, anchor: pos{2, 2}, head: pos{0, 6}}
	if r.text(plain) != s.text(plain) {
		t.Fatal("reverse drag differs")
	}
}

func TestKillPromptFlow(t *testing.T) {
	var tm tea.Model = loaded(t, 120, 40)
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m := tm.(Model)
	if m.kill == nil {
		t.Fatal("x did not open the kill prompt")
	}
	if out := ansi.Strip(m.View()); !strings.Contains(out, "Terminate") {
		t.Fatalf("prompt not rendered:\n%s", out)
	}
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if tm.(Model).kill != nil {
		t.Fatal("esc did not cancel")
	}
}

func TestKillBlocksPID1AndSelf(t *testing.T) {
	ps, _ := proc.List(context.Background())
	for _, p := range ps {
		if p.PID == 1 {
			k := newKillPrompt(p, ps, nil)
			if k.blocked == "" {
				t.Fatal("launchd must not be killable")
			}
		}
	}
}

func TestKillSendsSignalToRealProcess(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	ps, _ := proc.List(context.Background())
	var target proc.Process
	for _, p := range ps {
		if p.PID == cmd.Process.Pid {
			target = p
		}
	}
	if target.PID == 0 {
		t.Fatal("sleep not in process list")
	}
	msg := sendSignal(syscall.SIGTERM, []proc.Process{target})
	if msg.err != nil {
		t.Fatalf("kill failed: %v", msg.err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("process exited cleanly; expected signal")
	}
}

func TestDescendants(t *testing.T) {
	all := []proc.Process{{PID: 1, PPID: 0}, {PID: 2, PPID: 1}, {PID: 3, PPID: 2}, {PID: 4, PPID: 3}, {PID: 9, PPID: 1}}
	d := descendants(all, 2)
	if len(d) != 2 || d[0].PID != 3 || d[1].PID != 4 {
		t.Fatalf("%+v", d)
	}
}

func TestOverlayKeepsDimensions(t *testing.T) {
	base := strings.Repeat(strings.Repeat(".", 40)+"\n", 9) + strings.Repeat(".", 40)
	out := overlay(base, "┌──┐\n│hi│\n└──┘", 40, 10)
	lines := strings.Split(out, "\n")
	if len(lines) != 10 {
		t.Fatalf("%d lines", len(lines))
	}
	for _, l := range lines {
		if ansi.StringWidth(l) != 40 {
			t.Fatalf("width %d: %q", ansi.StringWidth(l), l)
		}
	}
}
