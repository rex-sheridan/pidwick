package proc

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"howett.net/plist"
)

// Job is a launchd job definition that can start a process.
type Job struct {
	Label       string
	PlistPath   string
	Program     string
	Args        []string
	RunAtLoad   bool
	KeepAlive   bool
	OnDemand    []string // human-readable launch triggers
	Domain      string   // "system daemon" or "per-user agent"
	AppleSigned bool     // lives in Apple-owned plist dir
}

var (
	jobsOnce  sync.Once
	jobsByExe map[string]*Job
	jobsByLbl map[string]*Job
)

func launchdDirs() []string {
	dirs := []string{
		"/System/Library/LaunchDaemons",
		"/System/Library/LaunchAgents",
		"/Library/Apple/System/Library/LaunchDaemons",
		"/Library/Apple/System/Library/LaunchAgents",
		"/Library/LaunchDaemons",
		"/Library/LaunchAgents",
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Library/LaunchAgents"))
	}
	return dirs
}

func loadJobs() {
	jobsByExe = map[string]*Job{}
	jobsByLbl = map[string]*Job{}
	for _, dir := range launchdDirs() {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".plist") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var d map[string]any
			if _, err := plist.Unmarshal(data, &d); err != nil {
				continue
			}
			j := &Job{PlistPath: path}
			j.Label, _ = d["Label"].(string)
			j.Program, _ = d["Program"].(string)
			if args, ok := d["ProgramArguments"].([]any); ok {
				for _, a := range args {
					if s, ok := a.(string); ok {
						j.Args = append(j.Args, s)
					}
				}
			}
			exe := j.Program
			if exe == "" && len(j.Args) > 0 {
				exe = j.Args[0]
			}
			j.RunAtLoad, _ = d["RunAtLoad"].(bool)
			switch v := d["KeepAlive"].(type) {
			case bool:
				j.KeepAlive = v
			case map[string]any:
				j.KeepAlive = true
			}
			if _, ok := d["StartInterval"]; ok {
				j.OnDemand = append(j.OnDemand, "on a repeating timer")
			}
			if _, ok := d["StartCalendarInterval"]; ok {
				j.OnDemand = append(j.OnDemand, "on a schedule")
			}
			if _, ok := d["WatchPaths"]; ok {
				j.OnDemand = append(j.OnDemand, "when watched files change")
			}
			if _, ok := d["QueueDirectories"]; ok {
				j.OnDemand = append(j.OnDemand, "when files appear in a queue folder")
			}
			if _, ok := d["MachServices"]; ok {
				j.OnDemand = append(j.OnDemand, "when another program contacts its Mach service")
			}
			if _, ok := d["Sockets"]; ok {
				j.OnDemand = append(j.OnDemand, "when a network connection arrives on its socket")
			}
			if _, ok := d["LaunchEvents"]; ok {
				j.OnDemand = append(j.OnDemand, "when a system event fires")
			}
			if strings.Contains(dir, "Daemons") {
				j.Domain = "system-wide background daemon (runs as root unless it sets another user)"
			} else {
				j.Domain = "per-user agent (starts when you log in)"
			}
			j.AppleSigned = strings.HasPrefix(dir, "/System/") || strings.HasPrefix(dir, "/Library/Apple/")
			if exe != "" {
				if _, dup := jobsByExe[exe]; !dup {
					jobsByExe[exe] = j
				}
			}
			if j.Label != "" {
				jobsByLbl[j.Label] = j
			}
		}
	}
}

// JobForExecutable finds a launchd job that runs the given executable.
func JobForExecutable(exe string) *Job {
	jobsOnce.Do(loadJobs)
	return jobsByExe[exe]
}

// JobForLabel finds a launchd job by label.
func JobForLabel(label string) *Job {
	jobsOnce.Do(loadJobs)
	return jobsByLbl[label]
}
