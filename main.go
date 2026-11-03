// Pidwick: understand what the processes on your Mac are, who made them,
// where they live, and what they are touching.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rex-sheridan/pidwick/internal/proc"
	"github.com/rex-sheridan/pidwick/internal/ui"
)

func main() {
	unknown := flag.Bool("unknown", false, "list running processes Pidwick has no description for, then exit")
	flag.Parse()
	if *unknown {
		os.Exit(listUnknown())
	}
	p := tea.NewProgram(ui.New(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "pidwick:", err)
		os.Exit(1)
	}
}

// listUnknown prints the gaps in the built-in knowledge base for this machine.
func listUnknown() int {
	ps, err := proc.List(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "pidwick:", err)
		return 1
	}
	gaps := proc.FindUnexplained(ps)
	for _, u := range gaps {
		fmt.Printf("%3d  %s  %-12s %-40s %s\n", u.Count, u.Kind.Tag(), u.User, u.Name, u.Path)
	}
	fmt.Printf("%d of %d processes have no description (%d distinct names)\n", countAll(gaps), len(ps), len(gaps))
	return 0
}

func countAll(gaps []proc.Unexplained) int {
	n := 0
	for _, u := range gaps {
		n += u.Count
	}
	return n
}
