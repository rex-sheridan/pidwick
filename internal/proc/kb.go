package proc

import (
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed kb/*.json
var kbFS embed.FS

// Entry is built-in knowledge about a well-known process.
type Entry struct {
	Summary string `json:"summary"`
	Why     string `json:"why"`
	Quit    string `json:"quit"`
	Vendor  string `json:"vendor,omitempty"`
	// Inferred marks entries whose purpose is deduced from the name and the
	// framework they live in rather than from Apple documentation.
	Inferred bool `json:"inferred,omitempty"`
}

var kb = map[string]Entry{}

func init() {
	files, err := kbFS.ReadDir("kb")
	if err != nil {
		panic(err)
	}
	for _, f := range files {
		data, err := kbFS.ReadFile("kb/" + f.Name())
		if err != nil {
			panic(err)
		}
		var m map[string]Entry
		if err := json.Unmarshal(data, &m); err != nil {
			panic(fmt.Sprintf("pidwick: bad kb/%s: %v", f.Name(), err))
		}
		for k, v := range m {
			kb[k] = v
		}
	}
}

const quitOnDemand = "Safe. It only runs on demand and macOS starts it again when something needs it."

type pattern struct {
	re    *regexp.Regexp
	build func(m []string) Entry
}

func appleGeneric(summary, why string) Entry {
	return Entry{Vendor: "Apple", Summary: summary, Why: why, Quit: quitOnDemand}
}

// patterns cover families of processes that macOS spawns by the dozen, such
// as widget and App Intents extensions.
var patterns = []pattern{
	{regexp.MustCompile(`^Core Audio Driver \((.+)\)$`), func(m []string) Entry {
		return Entry{
			Summary: "An audio driver plug-in loaded into Core Audio. It adds a virtual microphone/speaker (" + m[1] + ") so apps can route sound through the program that installed it.",
			Why:     "Installed by a conferencing or audio app (Zoom, Teams, BlackHole, Loopback, and so on). coreaudiod hosts it so it's present even when the app isn't open.",
			Quit:    "Leave it. Removing it means uninstalling the app that provides it; macOS will otherwise relaunch it.",
		}
	}},
	{regexp.MustCompile(`^com\.apple\.DriverKit-(.+)$`), func(m []string) Entry {
		return appleGeneric("A DriverKit driver ("+m[1]+"): a hardware driver that runs as an ordinary sandboxed process instead of inside the kernel, which is safer than the old kernel extensions.",
			"Loaded because matching hardware is present or built in to your Mac.")
	}},
	{regexp.MustCompile(`^com\.apple\.fskit\.(.+)$`), func(m []string) Entry {
		return appleGeneric("An FSKit filesystem module for "+m[1]+" volumes. It lets macOS read and write that disk format entirely in user space.",
			"Started when a disk or disk image using that format is mounted, or when macOS probes a drive.")
	}},
	{regexp.MustCompile(`(?i)(Wallpaper.*Extension|NeptuneOneWallpaper)$`), func(m []string) Entry {
		return appleGeneric("A wallpaper provider extension. Each family of built-in wallpapers (dynamic, aerial, image, gradient) is served by its own small process for the Wallpaper agent.",
			"Started by the desktop-wallpaper system when you set or preview wallpapers, or when the desktop needs to draw one.")
	}},
	{regexp.MustCompile(`(?i)Widgets?(Extension|_mac|_macOSExtension|Secure|s)?(\.macOS)?$`), func(m []string) Entry {
		return Entry{
			Summary: "A widget extension: a small process that supplies the content for a desktop or Notification Center widget. macOS runs it separately so the widget can refresh even when the app that owns it is closed.",
			Why:     "You have (or macOS previews) a widget from this app. Widget processes start briefly on a schedule or when the widget gallery is open.",
			Quit:    quitOnDemand,
		}
	}},
	{regexp.MustCompile(`(?i)(Intents?(Extension)?|AppIntents.*|IntentsHelperService|Intents_macOS)$`), func(m []string) Entry {
		return Entry{
			Summary: "An App Intents extension: lets Siri, Spotlight, Shortcuts and System Settings search for and trigger actions (\"turn on Dark Mode\", \"open Wi-Fi settings\") that belong to this app or settings pane.",
			Why:     "Spawned on demand while Spotlight, Siri, Shortcuts or System Settings is indexing or running actions.",
			Quit:    quitOnDemand,
		}
	}},
	{regexp.MustCompile(`(?i)Storage ?(Extension|Management( ?Extension)?)$`), func(m []string) Entry {
		return Entry{
			Summary: "A Storage Management plug-in. It reports how much disk space this app's data uses so it shows up under System Settings > General > Storage.",
			Why:     "Runs briefly when you open the Storage settings pane (or when macOS recalculates usage).",
			Quit:    quitOnDemand,
		}
	}},
	{regexp.MustCompile(`(?i)SpotlightIndexExtension|SpotlightExtension$`), func(m []string) Entry {
		return appleGeneric("A Spotlight index extension: lets an app hand its own content (notes, messages, mail) to Spotlight so it can be searched system-wide.",
			"Started by Spotlight's indexing services when that app's data changes.")
	}},
	{regexp.MustCompile(`(?i)SettingsSearch(Extension|TermsIntents)|SearchTerms`), func(m []string) Entry {
		return appleGeneric("Feeds search terms for one System Settings pane into Spotlight, so searching \"battery\" or \"VPN\" finds the right setting.",
			"Started when System Settings builds or refreshes its search index.")
	}},
	{regexp.MustCompile(`(?i)Settings(Extension)?$`), func(m []string) Entry {
		return appleGeneric("A System Settings extension: the code behind one pane or section of the System Settings app.",
			"Launched when you open System Settings (or Spotlight searches it); closes shortly after.")
	}},
}

func init() {
	patterns = append(patterns, pattern{regexp.MustCompile(`^iTermServer-`), func(m []string) Entry {
		return Entry{Vendor: "George Nachman (iTerm2)",
			Summary: "iTerm2's session server. It owns your terminal sessions so they survive if iTerm2 itself quits or restarts.",
			Why:     "Started by iTerm2 when 'Run jobs in servers' / session restoration is enabled.",
			Quit:    "Killing it ends the shells and programs running in those iTerm2 sessions."}
	}})
}

var helperRe = regexp.MustCompile(`^(.+?) Helper(?: \(([A-Za-z ]+)\))?$`)

// Lookup returns what Pidwick knows about a process by executable name.
func Lookup(name string) (Entry, bool) {
	if e, ok := kb[name]; ok {
		return e, true
	}
	for _, p := range patterns {
		if m := p.re.FindStringSubmatch(name); m != nil {
			return p.build(m), true
		}
	}
	if m := helperRe.FindStringSubmatch(name); m != nil {
		role := strings.ToLower(m[2])
		what := "a helper process for"
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

// LookupProcess tries the process name first, then the executable's file
// name (programs often rewrite their own name, e.g. a CLI that shows up as
// "ugrep" but is really "claude").
func LookupProcess(p Process) (Entry, bool) {
	if e, ok := Lookup(p.Name); ok {
		return e, true
	}
	if p.Path != "" {
		if base := filepath.Base(p.Path); base != p.Name {
			return Lookup(base)
		}
	}
	return Entry{}, false
}

// IsKnownApple reports whether the name is a built-in Apple process we know.
func IsKnownApple(name string) bool {
	e, ok := kb[name]
	return ok && e.Vendor == "Apple"
}
