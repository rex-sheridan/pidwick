package proc

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
)

//go:embed kb.json
var kbJSON []byte

// Entry is built-in knowledge about a well-known process.
type Entry struct {
	Summary string `json:"summary"`
	Why     string `json:"why"`
	Quit    string `json:"quit"`
	Vendor  string `json:"vendor,omitempty"`
}

var kb map[string]Entry

func init() {
	if err := json.Unmarshal(kbJSON, &kb); err != nil {
		panic("pidwick: bad kb.json: " + err.Error())
	}
}

var helperRe = regexp.MustCompile(`^(.+?) Helper(?: \(([A-Za-z ]+)\))?$`)

// Lookup returns what Pidwick knows about a process by executable name.
func Lookup(name string) (Entry, bool) {
	if e, ok := kb[name]; ok {
		return e, true
	}
	if m := helperRe.FindStringSubmatch(name); m != nil {
		role := strings.ToLower(m[2])
		what := "a helper process"
		switch role {
		case "renderer":
			what = "a renderer: it draws and runs the web content/UI of"
		case "gpu":
			what = "the GPU process: it does graphics work for"
		case "plugin":
			what = "a plugin host for"
		case "alerts":
			what = "the notifications helper for"
		case "":
			what = "a helper process for"
		default:
			what = "a \"" + role + "\" helper for"
		}
		return Entry{
			Summary: "This is " + what + " " + m[1] + ". Browsers and Electron apps (Chrome, Slack, VS Code, Discord…) split their work into many helper processes so a crash or hang in one doesn't take everything down.",
			Why:     "Started by " + m[1] + ". You will normally see several of them while that app is open.",
			Quit:    "Quitting " + m[1] + " ends its helpers. Force-quitting a single helper usually just closes one tab/window or makes the app respawn it.",
		}, true
	}
	return Entry{}, false
}

// IsKnownApple reports whether the name is a built-in Apple process we know.
func IsKnownApple(name string) bool {
	e, ok := kb[name]
	return ok && e.Vendor == "Apple"
}
