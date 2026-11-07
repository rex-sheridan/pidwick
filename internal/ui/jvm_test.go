package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rex-sheridan/pidwick/internal/proc"
)

func key(tm tea.Model, s string) tea.Model {
	var k tea.KeyMsg
	switch s {
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		k = tea.KeyMsg{Type: tea.KeyTab}
	default:
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	tm, _ = tm.Update(k)
	return tm
}

// javaModel has one Java process (alpha, selected) and one other (bravo).
func javaModel() tea.Model {
	tm := fakeModel()
	m := tm.(Model)
	p, _ := m.selected()
	p.UID = os.Getuid()
	m.view[0] = p
	m.cache[p.Key()] = cached{info: &proc.Info{Proc: p, Launch: proc.Launch{Runtime: "Java"}}}
	return m
}

func TestJVMTabOnlyForJava(t *testing.T) {
	m := fakeModel().(Model)
	if m.tabVisible(tabJVM) {
		t.Fatal("JVM tab visible for a non-Java process")
	}
	var tm tea.Model = m
	tm = key(tm, "4")
	if tm.(Model).tab != tabOverview {
		t.Fatalf("key 4 switched to tab %d for a non-Java process", tm.(Model).tab)
	}
	if strings.Contains(tm.View(), "4 JVM") {
		t.Error("tab bar shows 4 JVM for a non-Java process")
	}

	tm = javaModel()
	if !strings.Contains(tm.View(), "4 JVM") {
		t.Error("tab bar lacks 4 JVM for a Java process")
	}
	tm = key(tm, "4")
	if tm.(Model).tab != tabJVM {
		t.Fatalf("tab = %d, want JVM", tm.(Model).tab)
	}
	// moving to a non-Java process drops back to Overview
	tm = key(tm, "j")
	m = tm.(Model)
	if m.tab != tabOverview || m.tabVisible(tabJVM) {
		t.Errorf("tab = %d after selecting a non-Java process", m.tab)
	}
}

func TestTabCyclingSkipsHiddenJVM(t *testing.T) {
	var tm tea.Model = fakeModel()
	for i := 0; i < 3; i++ {
		tm = key(tm, "tab")
	}
	if tm.(Model).tab != tabOverview {
		t.Errorf("3 tabs on a non-Java process should wrap to Overview, got %d", tm.(Model).tab)
	}
	tm = javaModel()
	for i := 0; i < 3; i++ {
		tm = key(tm, "tab")
	}
	if tm.(Model).tab != tabJVM {
		t.Errorf("4th tab for Java should be JVM, got %d", tm.(Model).tab)
	}
}

func TestJVMActionsNeedConfirmation(t *testing.T) {
	tm := key(javaModel(), "4")
	for _, k := range []string{"g", "i"} {
		tm = key(tm, k)
		m := tm.(Model)
		if m.ask == nil {
			t.Fatalf("%s did not open a confirmation", k)
		}
		if !strings.Contains(tm.View(), "garbage collection") && !strings.Contains(tm.View(), "histogram") {
			t.Errorf("confirmation not drawn:\n%s", tm.View())
		}
		tm = key(tm, "esc")
		if tm.(Model).ask != nil || tm.(Model).pane.busy {
			t.Fatalf("esc should cancel %s without running anything", k)
		}
	}
}

func TestThreadDumpViewAndEscape(t *testing.T) {
	tm := key(javaModel(), "4")
	m := tm.(Model)
	p, _ := m.selected()
	tm = key(tm, "d")
	if !tm.(Model).pane.busy {
		t.Fatal("d did not start a thread dump")
	}
	tm, _ = tm.Update(jvmOutMsg{p.Key(), jvmThreads, "\"main\" #1\n   java.lang.Thread.State: RUNNABLE", nil})
	if out := tm.View(); !strings.Contains(out, "main") || !strings.Contains(out, "2 lines") {
		t.Errorf("dump not shown:\n%s", out)
	}
	tm = key(tm, "esc")
	if tm.(Model).pane.mode != jvmSummary {
		t.Error("esc did not return to the summary")
	}
}
