package ui

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/rex-sheridan/pidwick/internal/proc"
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
	tabJVM
)

// Screen layout (rows/cols) used to map mouse coordinates.
const (
	listTop   = 3 // first process row
	detailTop = 5 // first row of the detail viewport
	tabRow    = 3
	filterRow = 1
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
	jvmMsg   struct {
		key string
		jvm *proc.JVM
	}
)

type cachedJVM struct {
	jvm *proc.JVM
	at  time.Time
}

type cached struct {
	info *proc.Info
	at   time.Time
}

// ended is a selected process that has exited. It stays in the list and the
// detail pane, marked as ended, until the selection moves elsewhere.
type ended struct {
	p  proc.Process
	at time.Time
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
	jvm      map[string]cachedJVM // live JDK-tool data, fetched when the JVM tab is open
	jvmBusy  string               // key being attached to
	pane     jvmPane              // thread dump / histogram on the JVM tab
	ask      *jvmAsk              // pending confirmation (histogram, GC)
	loading  string               // key being inspected
	seq      int
	flash    string
	flashSeq int
	err      error

	lines    []string    // detail content, raw ANSI, one entry per row
	plain    []string    // same, ANSI-stripped and right-trimmed (for copying)
	links    map[int]int // detail line -> PID of the process a click on it opens
	sel      selection
	dragging bool
	lastClk  struct {
		at    time.Time
		x, y  int
		count int
	}
	ghost       *ended
	nativeMouse bool // mouse released so the terminal can select text itself
	kill        *killPrompt
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
		jvm:    map[string]cachedJVM{},
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

// keepEnded makes the selected process outlive its exit. If it has vanished
// from the latest scan it is kept as a ghost (frozen) so the
// view doesn't jump to another process (its last CPU value is kept so it
// holds its place in the sort, but the list shows "gone"); the ghost is dropped once the PID is
// live again or the selection moves.
func (m *Model) keepEnded(prev proc.Process, hadSel bool) {
	live := func(pid int) bool {
		for _, p := range m.all {
			if p.PID == pid {
				return true
			}
		}
		return false
	}
	if m.ghost != nil && live(m.ghost.p.PID) {
		m.ghost = nil // PID reused by a new process
	}
	if m.ghost == nil && hadSel && !live(prev.PID) {
		m.ghost = &ended{p: prev, at: time.Now()}
	}
	if m.ghost != nil {
		m.all = append(m.all[:len(m.all):len(m.all)], m.ghost.p)
	}
}

func (m Model) isEnded(p proc.Process) bool {
	return m.ghost != nil && p.PID == m.ghost.p.PID
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
			hay := strings.ToLower(fmt.Sprintf("%s %s %s %d %s", p.Name, p.Detail, p.Path, p.PID, p.User))
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
		found := false
		for i, p := range m.view {
			if p.PID == keep.PID {
				m.cursor, found = i, true
				break
			}
		}
		if !found {
			// The selected process was filtered out: start from the top
			// instead of landing wherever the old index happens to be.
			m.cursor, m.offset = 0, 0
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
	if p, ok := m.selected(); m.ghost != nil && (!ok || !m.isEnded(p)) {
		m.ghost = nil
	}
	if p, ok := m.selected(); !ok || m.pane.key != p.Key() {
		m.pane = jvmPane{}
	}
	m.detail.GotoTop()
	m.showCurrent()
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return debounceMsg{seq} })
}

func (m *Model) setContent(s string) {
	m.links = nil
	if strings.Join(m.lines, "\n") != s {
		m.sel = selection{}
	}
	m.detail.SetContent(s)
	m.lines = strings.Split(s, "\n")
	m.plain = make([]string, len(m.lines))
	for i, l := range m.lines {
		m.plain[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
}

func (m *Model) showCurrent() {
	if m.tab == tabJVM && !m.tabVisible(tabJVM) {
		m.tab = tabOverview
	}
	p, ok := m.selected()
	if !ok {
		m.setContent("")
		return
	}
	if c, ok := m.cache[p.Key()]; ok {
		text, links := m.renderTab(c.info)
		m.setContent(text)
		m.links = links
		return
	}
	m.setContent(sDim.Render(ansi.Truncate("Inspecting "+p.Name+"…", max(m.detail.Width, 10), "…")))
}

func (m Model) renderTab(in *proc.Info) (string, map[int]int) {
	w := m.detail.Width
	switch m.tab {
	case tabFiles:
		return renderFiles(in, w), nil
	case tabNetwork:
		return renderNetwork(in, w), nil
	case tabJVM:
		k := in.Proc.Key()
		if m.pane.key == k && m.pane.mode != jvmSummary {
			return renderJVMPane(in, m.pane, w), nil
		}
		return renderJVM(in, m.jvm[k].jvm, m.jvmBusy == k, w), nil
	}
	return renderOverview(in, w)
}

// ensureJVM attaches to the selected process with the JDK tools when the JVM
// tab is showing a Java process whose data is missing or stale. Browsing other
// tabs never attaches to anything.
func (m *Model) ensureJVM(force bool) tea.Cmd {
	if m.tab != tabJVM {
		return nil
	}
	p, ok := m.selected()
	if !ok || m.isEnded(p) {
		return nil
	}
	c, ok := m.cache[p.Key()]
	if !ok || !proc.IsJava(c.info.Launch) || m.jvmBusy == p.Key() {
		return nil
	}
	if j, ok := m.jvm[p.Key()]; ok && !force && time.Since(j.at) < 10*time.Second {
		return nil
	}
	m.jvmBusy = p.Key()
	m.showCurrent()
	info := c.info
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return jvmMsg{p.Key(), proc.InspectJVM(ctx, p, info.Launch, info.Handles, info.Cwd)}
	}
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
		m.keepEnded(prev, hadSel && !firstLoad)
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
		if c, ok := m.cache[p.Key()]; ok && (time.Since(c.at) < 8*time.Second || m.isEnded(p)) {
			return m, m.ensureJVM(false) // fresh enough, or the process is gone so there is nothing new to learn
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
		return m, m.ensureJVM(false)

	case jvmOutMsg:
		m.applyJVMOut(msg)
		if p, ok := m.selected(); ok && p.Key() == msg.key {
			y := m.detail.YOffset
			m.showCurrent()
			m.detail.SetYOffset(y)
		}
		return m, nil

	case jvmGCMsg:
		if msg.err != nil {
			return m, m.setFlash("GC failed: " + msg.err.Error())
		}
		delete(m.jvm, msg.key)
		return m, tea.Batch(m.setFlash(fmt.Sprintf("Full GC done: heap %s → %s", humanKB(msg.before), humanKB(msg.after))), m.ensureJVM(true))

	case jvmMsg:
		m.jvm[msg.key] = cachedJVM{msg.jvm, time.Now()}
		if m.jvmBusy == msg.key {
			m.jvmBusy = ""
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

	case killedMsg:
		cmds := []tea.Cmd{m.setFlash(msg.text)}
		if msg.err == nil {
			cmds = append(cmds, tea.Tick(700*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} }))
		}
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		return m.handleMouse(msg)

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
	if m.kill != nil {
		return m.handleKillKey(msg)
	}
	if m.ask != nil {
		return m.handleAskKey(msg)
	}
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
		// keys that can't be typed into the filter still work while it is focused
		case "ctrl+x":
			return m.openKill()
		case "tab":
			return m.setTab(m.nextTab(1))
		case "shift+tab":
			return m.setTab(m.nextTab(-1))
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.rebuild()
		return m, tea.Batch(cmd, m.onSelect())
	}

	if mm, cmd, ok := m.jvmKey(msg.String()); ok {
		return mm, cmd
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
		return m.setTab(m.nextTab(1))
	case "shift+tab", "h", "left":
		return m.setTab(m.nextTab(-1))
	case "1":
		return m.setTab(tabOverview)
	case "2":
		return m.setTab(tabFiles)
	case "3":
		return m.setTab(tabNetwork)
	case "4":
		if m.tabVisible(tabJVM) {
			return m.setTab(tabJVM)
		}
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
		if p, ok := m.selected(); ok && m.isEnded(p) {
			return m, m.setFlash(p.Name + " has ended; nothing left to rescan")
		} else if ok {
			delete(m.cache, p.Key())
			delete(m.jvm, p.Key())
			m.loading = p.Key()
			m.showCurrent()
			return m, inspect(p, m.all)
		}
	case "x", "delete":
		return m.openKill()
	case "m":
		m.nativeMouse = !m.nativeMouse
		if m.nativeMouse {
			return m, tea.Batch(tea.DisableMouse, m.setFlash("Mouse released: select text with your terminal. Press m to restore clicking"))
		}
		return m, tea.Batch(tea.EnableMouseCellMotion, m.setFlash("Mouse capture on"))
	case "y":
		if txt := strings.TrimRight(strings.Join(m.plain, "\n"), "\n "); txt != "" {
			return m, m.copyText(txt, "Copied this view")
		}
	case "f":
		if p, ok := m.selected(); ok && p.Path != "" {
			_ = exec.Command("open", "-R", p.Path).Start()
			return m, m.setFlash("Revealed in Finder")
		}
	case "c":
		if p, ok := m.selected(); ok && p.Path != "" {
			return m, m.copyText(p.Path, "Copied path to clipboard")
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

// openKill asks for confirmation before signalling the selected process.
func (m Model) openKill() (tea.Model, tea.Cmd) {
	p, ok := m.selected()
	if !ok {
		return m, nil
	}
	if m.isEnded(p) {
		// its PID may already belong to another process, so never signal it
		return m, m.setFlash(p.Name + " has already ended")
	}
	var in *proc.Info
	if c, ok := m.cache[p.Key()]; ok {
		in = c.info
	}
	m.kill = newKillPrompt(p, m.all, in)
	return m, nil
}

// visibleTabs lists the tabs that apply to the selected process: the JVM tab
// exists only for Java processes.
func (m Model) visibleTabs() []int {
	tabs := []int{tabOverview, tabFiles, tabNetwork}
	if m.selectedIsJava() {
		tabs = append(tabs, tabJVM)
	}
	return tabs
}

func (m Model) tabVisible(t int) bool {
	for _, v := range m.visibleTabs() {
		if v == t {
			return true
		}
	}
	return false
}

// nextTab steps through the visible tabs, wrapping around.
func (m Model) nextTab(dir int) int {
	tabs := m.visibleTabs()
	for i, t := range tabs {
		if t == m.tab {
			return tabs[(i+dir+len(tabs))%len(tabs)]
		}
	}
	return tabOverview
}

// selectedIsJava uses the inspected command line when we have it, and the
// executable's name until then, so the tab doesn't flicker while loading.
func (m Model) selectedIsJava() bool {
	p, ok := m.selected()
	if !ok {
		return false
	}
	if c, ok := m.cache[p.Key()]; ok {
		return proc.IsJava(c.info.Launch)
	}
	switch filepath.Base(p.Path) {
	case "java", "javaw":
		return true
	}
	return false
}

func (m Model) setTab(t int) (tea.Model, tea.Cmd) {
	m.tab = t
	y := 0
	m.detail.GotoTop()
	m.showCurrent()
	m.detail.SetYOffset(y)
	return m, m.ensureJVM(false)
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
	if m.nativeMouse {
		header += sWarn.Render("  · native select mode")
	}
	if m.flash != "" {
		header += "  " + sAccent.Render(m.flash)
	}

	left := m.renderList(lw)
	right := m.renderDetail(rw)
	sep := sDim.Render(strings.Repeat("│\n", m.height-3) + "│")
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " "+sep+" ", right)

	out := header + "\n" + body + "\n" + m.footer()
	if m.kill != nil {
		out = overlay(out, m.kill.view(m.width), m.width, m.height)
	}
	if m.ask != nil {
		out = overlay(out, m.ask.view(m.width), m.width, m.height)
	}
	return out
}

func (m Model) footer() string {
	if m.kill != nil {
		return sBad.Render("t terminate · k force kill · esc cancel")
	}
	if m.ask != nil {
		return sWarn.Render("y go ahead · esc cancel")
	}
	if m.filtering {
		return ansi.Truncate(sBold.Render("enter")+sDim.Render(" done typing (then x kills, s sorts…)")+sDim.Render(" · ")+
			sBold.Render("esc")+sDim.Render(" clear filter")+sDim.Render(" · ")+
			sBold.Render("↑↓")+sDim.Render(" select")+sDim.Render(" · ")+
			sBold.Render("ctrl+x")+sDim.Render(" kill")+sDim.Render(" · ")+
			sBold.Render("tab")+sDim.Render(" view"), m.width, "…")
	}
	// Hints are dropped right-to-left in this priority order when space is short.
	hints := []struct{ key, text string }{
		{"↑↓/click", "select"}, {"tab", "view"}, {"drag", "copy"}, {"/", "filter"}, {"x", "kill"},
		{"s", "sort"}, {"a", "hide Apple"}, {"c", "copy path"}, {"f", "reveal"}, {"w", "web"}, {"m", "native select"}, {"q", "quit"},
	}
	if m.filter.Value() != "" {
		hints = append([]struct{ key, text string }{{"esc", "clear filter"}}, hints...)
	}
	drop := []string{"native select", "web", "reveal", "copy path", "hide Apple", "sort", "clear filter"}
	render := func(hs []struct{ key, text string }) string {
		parts := make([]string, len(hs))
		for i, h := range hs {
			parts[i] = sBold.Render(h.key) + sDim.Render(" "+h.text)
		}
		return strings.Join(parts, sDim.Render(" · "))
	}
	for _, d := range drop {
		if ansi.StringWidth(render(hints)) <= m.width {
			break
		}
		for i, h := range hints {
			if h.text == d {
				hints = append(hints[:i], hints[i+1:]...)
				break
			}
		}
	}
	return ansi.Truncate(render(hints), m.width, "…")
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
		label := p.Name
		if p.Detail != "" {
			label += " ▸ " + p.Detail
		}
		gone := m.isEnded(p)
		if gone {
			label = "✗ " + label
		}
		name := ansi.Truncate(label, w-24, "…")
		tag := kindStyle(p.Kind).Render(p.Kind.Tag())
		cpu := fmt.Sprintf("%5.1f", p.CPU)
		if gone {
			cpu = " gone"
		}
		row := fmt.Sprintf("%s %-*s %6d %s %6s", tag, w-24, name, p.PID, cpu, humanKB(p.RSSKB))
		if gone && i != m.cursor {
			row = sDim.Render(ansi.Strip(row))
		}
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
	if m.isEnded(p) {
		meta = sDim.Render(fmt.Sprintf("PID %d · %s · ran for %s · last memory %s", p.PID, p.User, humanDur(p.Age), humanKB(p.RSSKB)))
	}
	if m.isEnded(p) {
		title = sBold.Render(p.Name) + "  " + sBad.Bold(true).Render("✗ ENDED "+humanDur(time.Since(m.ghost.at))+" ago") + sDim.Render("  (details below are from before it exited)")
	} else if c, ok := m.cache[p.Key()]; ok {
		title = sBold.Render(p.Name) + "  " + lipgloss.NewStyle().Foreground(levelColor(c.info.Verdict.Level)).Bold(true).Render("● "+c.info.Verdict.Level.String())
	} else {
		title = sBold.Render(p.Name) + "  " + sDim.Render("inspecting…")
	}
	head := ansi.Truncate(title, w, "…") + "\n" + ansi.Truncate(meta, w, "…") + "\n" + tabBar(m.visibleTabs(), m.tab, w) + "\n" + sDim.Render(strings.Repeat("─", w))
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Render(head + "\n" + m.detailRows())
}

// detailRows renders the visible slice of the detail content, applying the
// text-selection highlight.
func (m Model) detailRows() string {
	rows := make([]string, m.detail.Height)
	for r := range rows {
		i := m.detail.YOffset + r
		if i >= len(m.lines) {
			continue
		}
		l := ansi.Truncate(m.lines[i], m.detail.Width, "")
		if from, to, ok := m.sel.span(i, m.plain[i]); ok {
			l = highlight(l, from, to)
		}
		rows[r] = l
	}
	return strings.Join(rows, "\n")
}
