package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rex/pidwick/internal/proc"
)

func (m Model) detailX() int { return m.leftWidth() + 3 }

// detailPos maps a screen coordinate to a position in the detail content.
func (m Model) detailPos(x, y int) pos {
	row := min(max(y-detailTop, 0), m.detail.Height-1)
	line := min(max(m.detail.YOffset+row, 0), max(len(m.plain)-1, 0))
	col := max(x-m.detailX(), 0)
	if line < len(m.plain) {
		col = min(col, max(ansi.StringWidth(m.plain[line])-1, 0))
	}
	return pos{line, col}
}

func (m Model) inDetail(x, y int) bool {
	return x >= m.detailX() && y >= detailTop && y < detailTop+m.detail.Height
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.kill != nil || m.nativeMouse {
		return m, nil
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown:
		dir := 1
		if msg.Button == tea.MouseButtonWheelUp {
			dir = -1
		}
		if msg.X < m.leftWidth() {
			return m.move(dir * 3)
		}
		m.detail.SetYOffset(m.detail.YOffset + dir*3)
		return m, nil

	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		return m.mouseDown(msg)

	case msg.Action == tea.MouseActionMotion && m.dragging:
		// Scroll while dragging past the top or bottom edge.
		switch {
		case msg.Y < detailTop:
			m.detail.SetYOffset(m.detail.YOffset - 1)
		case msg.Y >= detailTop+m.detail.Height:
			m.detail.SetYOffset(m.detail.YOffset + 1)
		}
		m.sel.head = m.detailPos(msg.X, msg.Y)
		return m, nil

	case msg.Action == tea.MouseActionRelease && m.dragging:
		m.dragging = false
		if txt := m.sel.text(m.plain); txt != "" {
			return m, m.copyText(txt, "")
		}
		m.sel = selection{}
	}
	return m, nil
}

func (m Model) mouseDown(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// click counting for double/triple click
	now := time.Now()
	if now.Sub(m.lastClk.at) < 450*time.Millisecond && m.lastClk.x == msg.X && m.lastClk.y == msg.Y {
		m.lastClk.count++
	} else {
		m.lastClk.count = 1
	}
	m.lastClk.at, m.lastClk.x, m.lastClk.y = now, msg.X, msg.Y

	switch {
	case msg.X < m.leftWidth():
		if msg.Y == filterRow {
			m.filtering = true
			m.filter.Focus()
			return m, nil
		}
		idx := m.offset + msg.Y - listTop
		if msg.Y >= listTop && idx < len(m.view) && msg.Y < listTop+m.listHeight() {
			old := m.cursor
			m.cursor = idx
			m.clamp()
			if m.cursor != old {
				return m, m.onSelect()
			}
		}
		return m, nil

	case msg.Y == tabRow && msg.X >= m.detailX():
		if t := m.tabAt(msg.X - m.detailX()); t >= 0 {
			return m.setTab(t)
		}
		return m, nil

	case m.inDetail(msg.X, msg.Y):
		p := m.detailPos(msg.X, msg.Y)
		m.dragging = true
		m.sel = selection{on: true, anchor: p, head: p}
		if p.line < len(m.plain) {
			switch m.lastClk.count {
			case 2:
				a, b := wordAt(m.plain[p.line], p.col)
				m.sel.anchor, m.sel.head = pos{p.line, a}, pos{p.line, b}
			case 3:
				m.sel.anchor, m.sel.head = pos{p.line, 0}, pos{p.line, max(ansi.StringWidth(m.plain[p.line])-1, 0)}
			}
		}
	default:
		m.sel = selection{}
	}
	return m, nil
}

// tabAt returns which tab sits at column x of the tab bar, or -1.
func (m Model) tabAt(x int) int {
	sepW := 5
	if m.rightWidth() < 42 {
		sepW = 3
	}
	start := 0
	for i, n := range tabNames {
		end := start + len(n)
		if x >= start && x < end+sepW/2 {
			return i
		}
		start = end + sepW
	}
	return -1
}

// writeClipboard is a variable so tests don't clobber the real clipboard.
var writeClipboard = func(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

func (m *Model) copyText(txt, flash string) tea.Cmd {
	if err := writeClipboard(txt); err != nil {
		return m.setFlash("Couldn't copy: " + err.Error())
	}
	if flash == "" {
		flash = "Copied " + plural(len([]rune(txt)), "character") + " to clipboard"
	}
	return m.setFlash(flash)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func (m Model) handleKillKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.kill
	switch msg.String() {
	case "esc", "n", "q", "ctrl+c":
		m.kill = nil
		return m, nil
	}
	if k.blocked != "" {
		return m, nil
	}
	var sig syscall.Signal
	targets := []proc.Process{k.p}
	switch msg.String() {
	case "t", "enter":
		sig = syscall.SIGTERM
	case "k":
		sig = syscall.SIGKILL
	case "a":
		if len(k.kids) == 0 {
			return m, nil
		}
		sig = syscall.SIGTERM
		// children first so a supervising parent can't respawn them mid-way
		targets = nil
		for i := len(k.kids) - 1; i >= 0; i-- {
			targets = append(targets, k.kids[i])
		}
		targets = append(targets, k.p)
	default:
		return m, nil
	}
	m.kill = nil
	return m, func() tea.Msg { return sendSignal(sig, targets) }
}
