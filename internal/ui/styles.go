package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/rex-sheridan/pidwick/internal/proc"
)

var (
	cDim    = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}
	cText   = lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	cAccent = lipgloss.AdaptiveColor{Light: "27", Dark: "75"}
	cGood   = lipgloss.AdaptiveColor{Light: "28", Dark: "78"}
	cWarn   = lipgloss.AdaptiveColor{Light: "130", Dark: "214"}
	cBad    = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	cSystem = lipgloss.AdaptiveColor{Light: "61", Dark: "105"}
	cBorder = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
	cSelBg  = lipgloss.AdaptiveColor{Light: "153", Dark: "237"}

	sDim     = lipgloss.NewStyle().Foreground(cDim)
	sText    = lipgloss.NewStyle().Foreground(cText)
	sBold    = lipgloss.NewStyle().Foreground(cText).Bold(true)
	sAccent  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sGood    = lipgloss.NewStyle().Foreground(cGood)
	sWarn    = lipgloss.NewStyle().Foreground(cWarn)
	sBad     = lipgloss.NewStyle().Foreground(cBad)
	sSection = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62")).Padding(0, 1)
)

func levelColor(l proc.Level) lipgloss.AdaptiveColor {
	switch l {
	case proc.LevelSystem:
		return cSystem
	case proc.LevelTrusted:
		return cGood
	case proc.LevelUnverified:
		return cWarn
	case proc.LevelSuspicious:
		return cBad
	}
	return cDim
}

func badge(l proc.Level) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).
		Background(levelColor(l)).Padding(0, 1).Render(l.String())
}

func kindStyle(k proc.Kind) lipgloss.Style {
	switch k {
	case proc.KindSystem, proc.KindKernel:
		return lipgloss.NewStyle().Foreground(cSystem)
	case proc.KindApp:
		return lipgloss.NewStyle().Foreground(cGood)
	case proc.KindBrew, proc.KindUser:
		return lipgloss.NewStyle().Foreground(cWarn)
	case proc.KindTemp:
		return lipgloss.NewStyle().Foreground(cBad)
	}
	return sDim
}

func markGlyph(m proc.Mark) string {
	switch m {
	case proc.MarkGood:
		return sGood.Render("✓")
	case proc.MarkWarn:
		return sWarn.Render("!")
	case proc.MarkBad:
		return sBad.Render("✗")
	}
	return sDim.Render("·")
}
