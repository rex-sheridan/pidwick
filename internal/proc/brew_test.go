package proc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBrewFromSymlink(t *testing.T) {
	root := t.TempDir()
	keg := filepath.Join(root, "Cellar", "babashka", "1.2.3")
	os.MkdirAll(filepath.Join(keg, "bin"), 0o755)
	os.MkdirAll(filepath.Join(root, "bin"), 0o755)
	os.WriteFile(filepath.Join(keg, "bin", "bb"), nil, 0o755)
	os.Symlink("../Cellar/babashka/1.2.3/bin/bb", filepath.Join(root, "bin", "bb"))

	// The regexp only trusts real Homebrew prefixes, so a temp dir is not a keg...
	if b := brewFor(context.Background(), filepath.Join(root, "bin", "bb")); b != nil {
		t.Fatalf("temp dir treated as Homebrew: %+v", b)
	}
	// ...but a path already inside a prefix resolves to name and version.
	m := brewRe.FindStringSubmatch("/opt/homebrew/Cellar/babashka/1.2.3/bin/bb")
	if m == nil || m[3] != "babashka" || m[4] != "1.2.3" {
		t.Fatalf("brewRe = %v", m)
	}
}

func TestBrewMetadata(t *testing.T) {
	b := &Brew{Name: "babashka"}
	applyReceipt(b, []byte(`{"installed_on_request":true,"time":1793183424,"source":{"tap":"borkdude/brew"},"runtime_dependencies":[{"full_name":"openssl@3"}]}`))
	applyBrewInfo(b, []byte(`{"formulae":[{"desc":"Native Clojure interpreter","homepage":"https://github.com/babashka/babashka","license":"EPL-1.0","tap":"borkdude/brew"}],"casks":[]}`))
	if !b.OnRequest || b.Tap != "borkdude/brew" || b.Installed.IsZero() || len(b.Deps) != 1 || b.Desc == "" || b.License != "EPL-1.0" {
		t.Fatalf("%+v", b)
	}
	if got := brewPageURL(b); got != "https://github.com/borkdude/homebrew-brew" {
		t.Errorf("third-party page = %s", got)
	}
	if got := brewPageURL(&Brew{Name: "wget", Tap: "homebrew/core"}); got != "https://formulae.brew.sh/formula/wget" {
		t.Errorf("core page = %s", got)
	}
	if got := brewPageURL(&Brew{Name: "ant", Cask: true}); got != "https://formulae.brew.sh/cask/ant" {
		t.Errorf("cask page = %s", got)
	}
}

func TestCaskIndex(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Applications", "Foo.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	ver := filepath.Join(root, "prefix", "Caskroom", "foo", "2.0")
	os.MkdirAll(ver, 0o755)
	os.MkdirAll(filepath.Join(root, "prefix", "Caskroom", "foo", ".metadata"), 0o755)
	os.Symlink(app, filepath.Join(ver, "Foo.app"))
	os.WriteFile(filepath.Join(ver, "plain-file"), nil, 0o644) // not a link: ignored

	idx := buildCaskIndex([]string{filepath.Join(root, "prefix")})
	want, _ := filepath.EvalSymlinks(app)
	c, ok := idx[want]
	if !ok || c.token != "foo" || c.version != "2.0" || len(idx) != 1 {
		t.Fatalf("index = %+v", idx)
	}
}

func TestCaskReceipt(t *testing.T) {
	b := &Brew{Cask: true}
	applyReceipt(b, []byte(`{"installed_on_request":true,"time":1792579109,"runtime_dependencies":{},"source":{"tap":"homebrew/cask"}}`))
	if !b.OnRequest || b.Tap != "homebrew/cask" || b.Installed.IsZero() || len(b.Deps) != 0 {
		t.Fatalf("%+v", b)
	}
}
