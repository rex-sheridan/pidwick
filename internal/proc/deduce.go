package proc

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var frameworkRe = regexp.MustCompile(`/([A-Za-z0-9]+)\.framework/`)

// Framework returns the name of the framework a path lives in, if any.
func Framework(path string) string {
	if m := frameworkRe.FindStringSubmatch(path); m != nil {
		return m[1]
	}
	return ""
}

// Deduce lists clues about a process that has no knowledge-base entry,
// worked out from where it lives, how it is named, how launchd starts it and
// who its parent is. Each clue is a complete sentence, ordered most useful first.
func Deduce(in *Info) []string {
	var out []string
	p := in.Proc

	if fw := Framework(p.Path); fw != "" && p.Kind == KindSystem {
		out = append(out, "It ships inside Apple's "+fw+" framework, so it most likely supports that area of the system.")
	}
	switch lower := strings.ToLower(p.Name); {
	case strings.HasSuffix(lower, "agent") && len(lower) > 5:
		out = append(out, "The name ends in “agent”, which usually means a helper that runs inside your login session.")
	case strings.HasSuffix(lower, "d") && len(lower) > 3 && !strings.Contains(lower, " ") && !strings.Contains(lower, "."):
		out = append(out, "The name ends in “d”, the Unix convention for a background daemon.")
	case strings.HasSuffix(lower, "service") && len(lower) > 7:
		out = append(out, "The name ends in “service”, so another process asks it to do work on request.")
	}
	if j := in.Job; j != nil {
		s := fmt.Sprintf("launchd runs it as a %s (%s).", j.Domain, j.Label)
		switch {
		case j.KeepAlive:
			s += " It is kept running."
		case len(j.OnDemand) > 0:
			s += " It starts on demand: " + strings.Join(j.OnDemand, ", ") + "."
		case j.RunAtLoad:
			s += " It starts at login or boot."
		}
		out = append(out, s)
	} else if in.JobLabel != "" {
		out = append(out, "launchd knows it as “"+in.JobLabel+"”.")
	}
	if n := len(in.Lineage); n > 0 {
		if par := in.Lineage[n-1]; par.PID > 1 {
			out = append(out, fmt.Sprintf("It was started by %s (PID %d).", par.Name, par.PID))
		}
	}
	return out
}

// Unexplained is a group of same-named processes Pidwick has no entry for.
type Unexplained struct {
	Name  string
	Count int
	Path  string
	Kind  Kind
	User  string
}

// FindUnexplained returns the processes with no knowledge-base entry,
// grouped by name and sorted by name.
func FindUnexplained(ps []Process) []Unexplained {
	byName := map[string]*Unexplained{}
	for _, p := range ps {
		if _, ok := LookupProcess(p); ok {
			continue
		}
		u := byName[p.Name]
		if u == nil {
			u = &Unexplained{Name: p.Name, Path: p.Path, Kind: p.Kind, User: p.User}
			byName[p.Name] = u
		}
		u.Count++
	}
	out := make([]Unexplained, 0, len(byName))
	for _, u := range byName {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
