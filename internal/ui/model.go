package ui

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rex/pidwick/internal/proc"
)

type sortKey int

const (
	sortCPU sortKey = iota
	sortMem
	sortAge
	sortName
	sortPID
	sortCount
)

func (s sortKey) String() string {
	return [...]string{"CPU", "memory", "newest", "name", "PID"}[s]
}

const (
	tabOverview = iota
	tabFiles
	tabNetwork
	tabCount
)

type (
	procsMsg    struct{ procs []proc.Process }
	tickMsg     struct{}
	debounceMsg struct{ seq int }
	inspectMsg  struct {
		key  string
		info *proc.Info
	}
	flashMsg struct{ seq int }
)

type cached struct {
	info *proc.Info
	at   time.Time
}

type Model struct {
	width, height int

	all    []proc.Process
	view   []proc.Process
	cursor int
	offset int

	filter    textinput.Model
	filtering bool
	sortBy    sortKey
	hideApple bool

	tab      int
	detail   viewport.Model
	cache    map[string]cached
	loading  string // key being inspected
	seq      int
	flash    string
	flashSeq int
	err      error
}

func New() Model {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "filter by name, path, PID, user…"
	ti.PromptStyle = sAccent
	return Model{
		filter: ti,
		detail: viewport.New(10, 10),
		cache:  map[string]cached{},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchProcs(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func fetchProcs() tea.Cmd {
	return func() tea.Msg {
		ps, _ := proc.List(context.Background())
		return procsMsg{ps}
	}
}

func (m Model) selected() (proc.Process, bool) {
	if m.cursor < 0 || m.cursor >= len(m.view) {
		return proc.Process{}, false
	}
	return m.view[m.cursor], true
}

// rebuild recomputes the filtered, sorted view while keeping the same process selected.
func (m *Model) rebuild() {
	var keep proc.Process
	hadSel := false
	if p, ok := m.selected(); ok {
		keep, hadSel = p, true
	}
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	view := m.all[:0:0]
	for _, p := range m.all {
		if m.hideApple && (p.Kind == proc.KindSystem || p.Kind == proc.KindKernel) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(fmt.Sprintf("%s %s %d %s", p.Name, p.Path, p.PID, p.User))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		view = append(view, p)
	}
	sort.SliceStable(view, func(i, j int) bool {
		a, b := view[i], view[j]
		switch m.sortBy {
		case sortMem:
			return a.RSSKB > b.RSSKB
		case sortAge:
			return a.Age < b.Age
		case sortName:
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case sortPID:
			return a.PID < b.PID
		}
		if a.CPU != b.CPU {
			return a.CPU > b.CPU
		}
		return a.RSSKB > b.RSSKB
	})
	m.view = view
	if hadSel {
		for i, p := range m.view {
			if p.PID == keep.PID {
				m.cursor = i
				break
			}
		}
	}
	m.clamp()
}

func (m *Model) clamp() {
	if m.cursor >= len(m.view) {
		m.cursor = len(m.view) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m Model) listHeight() int { return max(1, m.height-4) }

func (m Model) leftWidth() int {
	lw := m.width * 40 / 100
	return min(max(lw, 42), 60)
}

func (m Model) rightWidth() int { return max(20, m.width-m.leftWidth()-3) }

// onSelect schedules a debounced inspection of the newly selected process.
func (m *Model) onSelect() tea.Cmd {
	m.seq++
	seq := m.seq
	m.detail.GotoTop()
	m.showCurrent()
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return debounceMsg{seq} })
}

func (m *Model) showCurrent() {
	p, ok := m.selected()
	if !ok {
		m.detail.SetContent("")
		return
	}
	if c, ok := m.cache[p.Key()]; ok {
		m.detail.SetContent(m.renderTab(c.info))
		return
	}
	m.detail.SetContent(sDim.Render("Inspecting " + p.Name + "…"))
}

func (m Model) renderTab(in *proc.Info) string {
	w := m.detail.Width
	switch m.tab {
	case tabFiles:
		return renderFiles(in, w)
	case tabNetwork:
		return renderNetwork(in, w)
	}
	return renderOverview(in, w)
}

func inspect(p proc.Process, all []proc.Process) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return inspectMsg{p.Key(), proc.Inspect(ctx, p, all)}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.detail.Width = m.rightWidth()
		m.detail.Height = max(1, m.height-6)
		m.filter.Width = m.leftWidth() - 4
		m.clamp()
		m.showCurrent()
		return m, nil

	case procsMsg:
		firstLoad := len(m.all) == 0
		prev, hadSel := m.selected()
		m.all = msg.procs
		m.rebuild()
		if cur, ok := m.selected(); firstLoad || !hadSel || (ok && cur.PID != prev.PID) {
			return m, m.onSelect()
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(fetchProcs(), tick())

	case debounceMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		p, ok := m.selected()
		if !ok {
			return m, nil
		}
		if c, ok := m.cache[p.Key()]; ok && time.Since(c.at) < 8*time.Second {
			return m, nil
		}
		m.loading = p.Key()
		return m, inspect(p, m.all)

	case inspectMsg:
		m.cache[msg.key] = cached{msg.info, time.Now()}
		if m.loading == msg.key {
			m.loading = ""
		}
		if p, ok := m.selected(); ok && p.Key() == msg.key {
			y := m.detail.YOffset
			m.showCurrent()
			m.detail.SetYOffset(y)
		}
		return m, nil

	case flashMsg:
		if msg.seq == m.flashSeq {
			m.flash = ""
		}
		return m, nil

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress || msg.Action == tea.MouseActionMotion {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				dir := 1
				if msg.Button == tea.MouseButtonWheelUp {
					dir = -1
				}
				if msg.X < m.leftWidth() {
					return m.move(dir * 3)
				}
				m.detail.SetYOffset(m.detail.YOffset + dir*3)
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) move(delta int) (tea.Model, tea.Cmd) {
	old := m.cursor
	m.cursor += delta
	m.clamp()
	if m.cursor != old {
		return m, m.onSelect()
	}
	return m, nil
}

func (m *Model) setFlash(s string) tea.Cmd {
	m.flash = s
	m.flashSeq++
	seq := m.flashSeq
	return tea.Tick(2500*time.Millisecond, func(time.Time) tea.Msg { return flashMsg{seq} })
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch msg.String() {
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.rebuild()
			return m, m.onSelect()
		case "up", "ctrl+p":
			return m.move(-1)
		case "down", "ctrl+n":
			return m.move(1)
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.rebuild()
		return m, tea.Batch(cmd, m.onSelect())
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		return m.move(-1)
	case "down", "j":
		return m.move(1)
	case "pgup":
		return m.move(-m.listHeight())
	case "pgdown":
		return m.move(m.listHeight())
	case "home":
		return m.move(-len(m.view))
	case "end":
		return m.move(len(m.view))
	case "K", "ctrl+u":
		m.detail.HalfViewUp()
	case "J", "ctrl+d":
		m.detail.HalfViewDown()
	case "tab", "l", "right":
		return m.setTab((m.tab + 1) % tabCount)
	case "shift+tab", "h", "left":
		return m.setTab((m.tab + tabCount - 1) % tabCount)
	case "1":
		return m.setTab(tabOverview)
	case "2":
		return m.setTab(tabFiles)
	case "3":
		return m.setTab(tabNetwork)
	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.rebuild()
			return m, m.onSelect()
		}
	case "s":
		m.sortBy = (m.sortBy + 1) % sortCount
		m.rebuild()
	case "a":
		m.hideApple = !m.hideApple
		m.rebuild()
		return m, m.onSelect()
	case "r":
		if p, ok := m.selected(); ok {
			delete(m.cache, p.Key())
			m.loading = p.Key()
			m.showCurrent()
			return m, inspect(p, m.all)
		}
	case "f":
		if p, ok := m.selected(); ok && p.Path != "" {
			_ = exec.Command("open", "-R", p.Path).Start()
			return m, m.setFlash("Revealed in Finder")
		}
	case "c":
		if p, ok := m.selected(); ok && p.Path != "" {
			cmd := exec.Command("pbcopy")
			cmd.Stdin = strings.NewReader(p.Path)
			if cmd.Run() == nil {
				return m, m.setFlash("Copied path to clipboard")
			}
		}
	case "w":
		if p, ok := m.selected(); ok {
			q := url.QueryEscape("macOS process " + p.Name + " what is it")
			_ = exec.Command("open", "https://duckduckgo.com/?q="+q).Start()
			return m, m.setFlash("Opened web search for " + p.Name)
		}
	}
	return m, nil
}

func (m Model) setTab(t int) (tea.Model, tea.Cmd) {
	m.tab = t
	y := 0
	m.detail.GotoTop()
	m.showCurrent()
	m.detail.SetYOffset(y)
	return m, nil
}

// ---- view ----

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.width < 80 || m.height < 12 {
		return "Pidwick needs at least an 80x12 terminal."
	}
	lw, rw := m.leftWidth(), m.rightWidth()

	header := sTitle.Render("pidwick") + sDim.Render(fmt.Sprintf("  %d of %d processes · sort: %s", len(m.view), len(m.all), m.sortBy))
	if m.hideApple {
		header += sWarn.Render("  · hiding Apple/system")
	}
	if m.flash != "" {
		header += "  " + sAccent.Render(m.flash)
	}

	left := m.renderList(lw)
	right := m.renderDetail(rw)
	sep := sDim.Render(strings.Repeat("│\n", m.height-3) + "│")
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " "+sep+" ", right)

	return header + "\n" + body + "\n" + m.footer()
}

func (m Model) footer() string {
	keys := "↑↓ select · tab/1-3 view · J/K scroll · / filter · s sort · a hide Apple · r rescan · f reveal · c copy · w web · q quit"
	return sDim.Render(ansi.Truncate(keys, m.width, "…"))
}

func (m Model) renderList(w int) string {
	var rows []string
	if m.filtering || m.filter.Value() != "" {
		rows = append(rows, m.filter.View())
	} else {
		rows = append(rows, sDim.Render(fmt.Sprintf("%-3s %-*s %6s %5s %6s", "", w-24, "PROCESS", "PID", "CPU", "MEM")))
	}
	rows = append(rows, sDim.Render(strings.Repeat("─", w)))
	h := m.listHeight()
	end := min(len(m.view), m.offset+h)
	for i := m.offset; i < end; i++ {
		p := m.view[i]
		name := ansi.Truncate(p.Name, w-24, "…")
		tag := kindStyle(p.Kind).Render(p.Kind.Tag())
		row := fmt.Sprintf("%s %-*s %6d %5.1f %6s", tag, w-24, name, p.PID, p.CPU, humanKB(p.RSSKB))
		if i == m.cursor {
			row = lipgloss.NewStyle().Background(cSelBg).Bold(true).Width(w).Render(row)
		}
		rows = append(rows, row)
	}
	if len(m.view) == 0 {
		rows = append(rows, sDim.Render("no matching processes"))
	}
	for len(rows) < h+2 {
		rows = append(rows, "")
	}
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Render(strings.Join(rows, "\n"))
}

func humanKB(kb int64) string {
	switch {
	case kb >= 1024*1024:
		return fmt.Sprintf("%.1fG", float64(kb)/1024/1024)
	case kb >= 1024:
		return fmt.Sprintf("%dM", kb/1024)
	}
	return fmt.Sprintf("%dK", kb)
}

func (m Model) renderDetail(w int) string {
	p, ok := m.selected()
	if !ok {
		return sDim.Render("Select a process")
	}
	title := ""
	meta := sDim.Render(fmt.Sprintf("PID %d · %s · up %s · CPU %.1f%% · %s", p.PID, p.User, humanDur(p.Age), p.CPU, humanKB(p.RSSKB)))
	if c, ok := m.cache[p.Key()]; ok {
		title = sBold.Render(p.Name) + "  " + lipgloss.NewStyle().Foreground(levelColor(c.info.Verdict.Level)).Bold(true).Render("● "+c.info.Verdict.Level.String())
	} else {
		title = sBold.Render(p.Name) + "  " + sDim.Render("inspecting…")
	}
	head := ansi.Truncate(title, w, "…") + "\n" + ansi.Truncate(meta, w, "…") + "\n" + tabBar(m.tab, w) + "\n" + sDim.Render(strings.Repeat("─", w))
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Render(head + "\n" + m.detail.View())
}
