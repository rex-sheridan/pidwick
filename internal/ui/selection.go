package ui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Terminals stop offering native text selection once an app captures the
// mouse, so Pidwick implements its own: drag, double-click (word) and
// triple-click (line) in the detail pane, copied on release.

type pos struct{ line, col int }

type selection struct {
	on           bool
	anchor, head pos
}

func (s selection) ordered() (pos, pos) {
	a, b := s.anchor, s.head
	if b.line < a.line || (b.line == a.line && b.col < a.col) {
		a, b = b, a
	}
	return a, b
}

func (s selection) empty() bool { return !s.on || s.anchor == s.head }

var selStyle = lipgloss.NewStyle().Reverse(true)

// span returns the half-open cell range [from,to) selected on a line.
func (s selection) span(line int, plain string) (int, int, bool) {
	if !s.on {
		return 0, 0, false
	}
	a, b := s.ordered()
	if line < a.line || line > b.line {
		return 0, 0, false
	}
	w := ansi.StringWidth(plain)
	from, to := 0, w
	if line == a.line {
		from = a.col
	}
	if line == b.line {
		to = b.col + 1
	}
	to = min(to, w)
	if from >= to {
		return 0, 0, false
	}
	return from, to, true
}

func highlight(line string, from, to int) string {
	w := ansi.StringWidth(line)
	to = min(to, w)
	if from >= to {
		return line
	}
	return ansi.Cut(line, 0, from) + selStyle.Render(ansi.Strip(ansi.Cut(line, from, to))) + ansi.Cut(line, to, w)
}

func (s selection) text(plain []string) string {
	if s.empty() {
		return ""
	}
	a, b := s.ordered()
	var out []string
	for i := a.line; i <= b.line && i < len(plain); i++ {
		from, to, ok := s.span(i, plain[i])
		if !ok {
			out = append(out, "")
			continue
		}
		out = append(out, strings.TrimRight(ansi.Strip(ansi.Cut(plain[i], from, to)), " "))
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n ")
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_-./:@~+%=", r)
}

// wordAt returns the inclusive cell range of the word under col.
func wordAt(plain string, col int) (int, int) {
	type cell struct {
		r    rune
		from int
		to   int
	}
	var cells []cell
	x := 0
	for _, r := range plain {
		w := ansi.StringWidth(string(r))
		cells = append(cells, cell{r, x, x + w})
		x += w
	}
	idx := -1
	for i, c := range cells {
		if col >= c.from && col < c.to {
			idx = i
			break
		}
	}
	if idx < 0 || !isWordRune(cells[idx].r) {
		return col, col
	}
	l, r := idx, idx
	for l > 0 && isWordRune(cells[l-1].r) {
		l--
	}
	for r < len(cells)-1 && isWordRune(cells[r+1].r) {
		r++
	}
	return cells[l].from, cells[r].to - 1
}
