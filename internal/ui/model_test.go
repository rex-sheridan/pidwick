package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rex/pidwick/internal/proc"
)

func loaded(t *testing.T, w, h int) Model {
	t.Helper()
	var m tea.Model = New()
	m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	ps, err := proc.List(context.Background())
	if err != nil || len(ps) == 0 {
		t.Fatalf("list: %v (%d)", err, len(ps))
	}
	m, _ = m.Update(procsMsg{ps})
	return m.(Model)
}

func TestViewFitsTerminal(t *testing.T) {
	for _, sz := range [][2]int{{100, 30}, {140, 42}, {80, 14}} {
		m := loaded(t, sz[0], sz[1])
		p, _ := m.selected()
		var tm tea.Model = m
		tm, _ = tm.Update(inspectMsg{p.Key(), proc.Inspect(context.Background(), p, m.all)})
		out := tm.View()
		lines := strings.Split(out, "\n")
		if len(lines) > sz[1] {
			t.Errorf("%v: %d lines > height", sz, len(lines))
		}
		for i, l := range lines {
			if ansi.StringWidth(l) > sz[0] {
				t.Errorf("%v: line %d width %d > %d: %q", sz, i, ansi.StringWidth(l), sz[0], ansi.Strip(l))
			}
		}
	}
}

func TestTabsAndFilter(t *testing.T) {
	var tm tea.Model = loaded(t, 120, 40)
	m := tm.(Model)
	for _, k := range []string{"2", "3", "1"} {
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	}
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	for _, r := range "launchd" {
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = tm.(Model)
	if len(m.view) == 0 || len(m.view) > 5 {
		t.Fatalf("filter 'launchd' gave %d rows", len(m.view))
	}
	p, _ := m.selected()
	tm, _ = tm.Update(inspectMsg{p.Key(), proc.Inspect(context.Background(), p, m.all)})
	t.Log("\n" + ansi.Strip(tm.View()))
	for _, k := range []string{"2", "3"} {
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEsc})
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		t.Log("\n" + ansi.Strip(tm.View()))
	}
}
