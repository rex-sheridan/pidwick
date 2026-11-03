package proc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Brew is what Pidwick can work out about the Homebrew package that owns a binary.
type Brew struct {
	Name      string // formula name or cask token
	Cask      bool
	Version   string
	Keg       string // resolved install directory
	Tap       string // e.g. homebrew/core or borkdude/brew
	Desc      string
	Homepage  string
	License   string
	PageURL   string // formulae.brew.sh page, or the tap's repository for third-party taps
	OnRequest bool   // installed directly by the user rather than as a dependency
	Installed time.Time
	Deps      []string // runtime dependencies
}

var brewRe = regexp.MustCompile(`^(/opt/homebrew|/usr/local|/home/linuxbrew/.linuxbrew)/(Cellar|Caskroom)/([^/]+)/([^/]+)`)

// brewFor walks back from an executable to its Homebrew package: it resolves
// the symlink (/opt/homebrew/bin/bb -> ../Cellar/babashka/1.13.229/bin/bb),
// reads the keg's install receipt, and asks brew for the description and
// homepage. It returns nil if the binary does not live in a keg.
func brewFor(ctx context.Context, path string) *Brew {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	var prefix string
	var b *Brew
	receipt := ""
	if m := brewRe.FindStringSubmatch(real); m != nil {
		prefix = m[1]
		b = &Brew{Name: m[3], Version: m[4], Cask: m[2] == "Caskroom", Keg: prefix + "/" + m[2] + "/" + m[3] + "/" + m[4]}
	} else if c, ok := caskOwner(real); ok {
		prefix = c.prefix
		b = &Brew{Name: c.token, Version: c.version, Cask: true, Keg: prefix + "/Caskroom/" + c.token + "/" + c.version}
	} else {
		return nil
	}
	receipt = b.Keg + "/INSTALL_RECEIPT.json"
	if b.Cask {
		// casks keep one receipt per token, next to (not inside) the version directory
		receipt = prefix + "/Caskroom/" + b.Name + "/.metadata/INSTALL_RECEIPT.json"
	}
	if data, err := os.ReadFile(receipt); err == nil {
		applyReceipt(b, data)
	}
	// every helper of one app shares a cask, so ask brew about each package once
	key := prefix + "|" + b.Keg
	out, ok := brewInfoCache.Load(key)
	if !ok {
		out, _ = brewInfoCache.LoadOrStore(key, brewInfoJSON(ctx, prefix+"/bin/brew", b))
	}
	if data, _ := out.([]byte); data != nil {
		applyBrewInfo(b, data)
	}
	b.PageURL = brewPageURL(b)
	return b
}

func applyReceipt(b *Brew, data []byte) {
	var r struct {
		OnRequest bool  `json:"installed_on_request"`
		Time      int64 `json:"time"`
		Source    struct {
			Tap string `json:"tap"`
		} `json:"source"`
		// an array of {full_name} for formulae, an object for casks
		Deps json.RawMessage `json:"runtime_dependencies"`
	}
	if json.Unmarshal(data, &r) != nil {
		return
	}
	var deps []struct {
		Name string `json:"full_name"`
	}
	_ = json.Unmarshal(r.Deps, &deps)
	b.OnRequest = r.OnRequest
	b.Tap = r.Source.Tap
	if r.Time > 0 {
		b.Installed = time.Unix(r.Time, 0)
	}
	for _, d := range deps {
		b.Deps = append(b.Deps, d.Name)
	}
}

// brewInfoJSON runs `brew info --json=v2`. Auto-update and analytics are
// switched off so it stays read-only and fast (about a second).
func brewInfoJSON(ctx context.Context, brew string, b *Brew) []byte {
	if _, err := os.Stat(brew); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	kind := "--formula"
	if b.Cask {
		kind = "--cask"
	}
	cmd := exec.CommandContext(ctx, brew, "info", "--json=v2", kind, b.Name)
	cmd.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_ENV_HINTS=1")
	out, err := cmd.Output() // stdout only: warnings on stderr would corrupt the JSON
	if err != nil {
		return nil
	}
	return out
}

func applyBrewInfo(b *Brew, data []byte) {
	var r struct {
		Formulae []struct {
			Desc     string `json:"desc"`
			Homepage string `json:"homepage"`
			License  string `json:"license"`
			Tap      string `json:"tap"`
		} `json:"formulae"`
		Casks []struct {
			Desc     string `json:"desc"`
			Homepage string `json:"homepage"`
			Tap      string `json:"tap"`
		} `json:"casks"`
	}
	if json.Unmarshal(data, &r) != nil {
		return
	}
	switch {
	case len(r.Formulae) > 0:
		f := r.Formulae[0]
		b.Desc, b.Homepage, b.License = f.Desc, f.Homepage, f.License
		if b.Tap == "" {
			b.Tap = f.Tap
		}
	case len(r.Casks) > 0:
		c := r.Casks[0]
		b.Desc, b.Homepage = c.Desc, c.Homepage
		if b.Tap == "" {
			b.Tap = c.Tap
		}
	}
}

// brewPageURL is the package's page: formulae.brew.sh for the official taps,
// otherwise the GitHub repository that hosts the tap.
func brewPageURL(b *Brew) string {
	kind := "formula"
	if b.Cask {
		kind = "cask"
	}
	switch b.Tap {
	case "", "homebrew/core", "homebrew/cask":
		return "https://formulae.brew.sh/" + kind + "/" + b.Name
	}
	if user, repo, ok := strings.Cut(b.Tap, "/"); ok {
		return "https://github.com/" + user + "/homebrew-" + repo
	}
	return ""
}

var brewInfoCache sync.Map // prefix|keg -> []byte (nil when brew was unavailable)

type caskEntry struct{ token, version, prefix string }

var (
	caskOnce  sync.Once
	caskIndex map[string]caskEntry // resolved app/binary path -> cask
)

// caskOwner finds the cask that installed the application or binary at path
// (or at one of its parent directories). Homebrew leaves a link to each
// installed artifact at Caskroom/<token>/<version>/<Name.app>; resolving
// those links gives the map from /Applications/Foo.app back to the cask.
func caskOwner(path string) (caskEntry, bool) {
	caskOnce.Do(func() { caskIndex = buildCaskIndex([]string{"/opt/homebrew", "/usr/local"}) })
	for p := path; p != "/" && p != "." && p != ""; p = filepath.Dir(p) {
		if c, ok := caskIndex[p]; ok {
			return c, true
		}
	}
	return caskEntry{}, false
}

func buildCaskIndex(prefixes []string) map[string]caskEntry {
	idx := map[string]caskEntry{}
	for _, prefix := range prefixes {
		room := prefix + "/Caskroom"
		tokens, _ := os.ReadDir(room)
		for _, tok := range tokens {
			vers, _ := os.ReadDir(filepath.Join(room, tok.Name()))
			for _, ver := range vers {
				if strings.HasPrefix(ver.Name(), ".") {
					continue // .metadata
				}
				dir := filepath.Join(room, tok.Name(), ver.Name())
				arts, _ := os.ReadDir(dir)
				for _, a := range arts {
					if a.Type()&os.ModeSymlink == 0 {
						continue // real files inside the keg are matched by brewRe
					}
					if target, err := filepath.EvalSymlinks(filepath.Join(dir, a.Name())); err == nil {
						idx[target] = caskEntry{tok.Name(), ver.Name(), prefix}
					}
				}
			}
		}
	}
	return idx
}
