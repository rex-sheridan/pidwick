// Pidwick: understand what the processes on your Mac are, who made them,
// where they live, and what they are touching.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rex-sheridan/pidwick/internal/ui"
)

func main() {
	p := tea.NewProgram(ui.New(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "pidwick:", err)
		os.Exit(1)
	}
}
