package proc

import (
	"testing"
	"time"
)

func TestPeelKeepsPathsWithSpaces(t *testing.T) {
	f, rest := peel("  12   1 501 0.0 100 01:02 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome", 6)
	if len(f) != 6 || f[0] != "12" || f[5] != "01:02" {
		t.Fatalf("fields = %v", f)
	}
	if rest != "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
		t.Fatalf("rest = %q", rest)
	}
}

func TestParseEtime(t *testing.T) {
	cases := map[string]time.Duration{
		"00:05":      5 * time.Second,
		"01:02:03":   time.Hour + 2*time.Minute + 3*time.Second,
		"3-04:00:00": 76 * time.Hour,
	}
	for in, want := range cases {
		if got := parseEtime(in); got != want {
			t.Errorf("parseEtime(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseCodesign(t *testing.T) {
	dev := parseCodesign("Identifier=com.foo.bar\nAuthority=Developer ID Application: Foo, Inc. (ABCDE12345)\nAuthority=Developer ID Certification Authority\nAuthority=Apple Root CA\nTeamIdentifier=ABCDE12345\nCodeDirectory v=20500 flags=0x10000(runtime)\n")
	if dev.Kind != SignerDeveloperID || dev.Signer != "Foo, Inc." || dev.TeamID != "ABCDE12345" || !dev.Hardened {
		t.Errorf("developer id: %+v", dev)
	}
	apple := parseCodesign("Identifier=com.apple.ls\nAuthority=macOS Software Signing\nPlatform identifier=26\n")
	if apple.Kind != SignerApple {
		t.Errorf("apple: %+v", apple)
	}
	adhoc := parseCodesign("Identifier=x\nSignature=adhoc\nTeamIdentifier=not set\n")
	if adhoc.Kind != SignerAdhoc || adhoc.TeamID != "" {
		t.Errorf("adhoc: %+v", adhoc)
	}
	if parseCodesign("/x: code object is not signed at all").Signed {
		t.Error("unsigned reported as signed")
	}
	mas := parseCodesign("Authority=Apple Mac OS Application Signing\nAuthority=Apple Worldwide Developer Relations Certification Authority\n")
	if mas.Kind != SignerAppStore {
		t.Errorf("app store: %+v", mas)
	}
}

func TestParseLsof(t *testing.T) {
	out := "p1\nfcwd\ntDIR\nn/tmp\nftxt\ntREG\nn/bin/zsh\nf3\ntREG\nn/tmp/a.log\nf4\ntPIPE\nn->0x1\n" +
		"f5\ntIPv4\nPTCP\nn*:8080\nTST=LISTEN\n" +
		"f6\ntIPv4\nPTCP\nn10.0.0.2:5->1.2.3.4:443\nTST=ESTABLISHED\n"
	h := parseLsof(out)
	if h.Cwd != "/tmp" || len(h.Libs) != 1 || len(h.Files) != 1 || h.Pipes != 1 || len(h.Conns) != 2 {
		t.Fatalf("%+v", h)
	}
	if h.Conns[0].State != "LISTEN" || h.Conns[0].Scope != "any" {
		t.Errorf("listener: %+v", h.Conns[0])
	}
	if h.Conns[1].Remote != "1.2.3.4:443" || h.Conns[1].Scope != "public" {
		t.Errorf("conn: %+v", h.Conns[1])
	}
}

func TestScopeOf(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:80": "loopback", "[::1]:80": "loopback", "192.168.1.5:22": "LAN",
		"8.8.8.8:53": "public", "[fe80::1]:5": "LAN",
	} {
		if got := scopeOf(addr, ""); got != want {
			t.Errorf("scopeOf(%q) = %s, want %s", addr, got, want)
		}
	}
}

func TestHelperLookup(t *testing.T) {
	e, ok := Lookup("Slack Helper (Renderer)")
	if !ok || e.Summary == "" {
		t.Fatal("helper pattern not recognised")
	}
	if _, ok := Lookup("definitely-not-a-process"); ok {
		t.Fatal("unexpected match")
	}
}

func TestJudge(t *testing.T) {
	base := func() *Info {
		return &Info{Proc: Process{PID: 500, Name: "thing", Path: "/Applications/Thing.app/Contents/MacOS/thing", Kind: KindApp}, Exists: true}
	}
	t.Run("developer id", func(t *testing.T) {
		in := base()
		in.Sign = Signature{Signed: true, Kind: SignerDeveloperID, Signer: "Foo", TeamID: "X", Verified: true}
		if v := Judge(in); v.Level != LevelTrusted {
			t.Errorf("level = %v", v.Level)
		}
	})
	t.Run("unsigned in tmp", func(t *testing.T) {
		in := base()
		in.Proc.Path, in.Proc.Kind = "/private/tmp/x", KindTemp
		if v := Judge(in); v.Level != LevelSuspicious {
			t.Errorf("level = %v", v.Level)
		}
	})
	t.Run("apple impersonator", func(t *testing.T) {
		in := base()
		in.Proc.Name, in.Proc.Path, in.Proc.Kind = "launchd", "/Users/x/Library/launchd", KindUser
		in.Sign = Signature{Signed: true, Kind: SignerAdhoc}
		if v := Judge(in); v.Level != LevelSuspicious {
			t.Errorf("level = %v", v.Level)
		}
	})
	t.Run("deleted binary", func(t *testing.T) {
		in := base()
		in.Exists = false
		if v := Judge(in); v.Level != LevelSuspicious {
			t.Errorf("level = %v", v.Level)
		}
	})
	t.Run("homebrew adhoc stays unverified", func(t *testing.T) {
		in := base()
		in.Proc.Path, in.Proc.Kind = "/opt/homebrew/bin/jq", KindBrew
		in.Sign = Signature{Signed: true, Kind: SignerAdhoc}
		if v := Judge(in); v.Level != LevelUnverified {
			t.Errorf("level = %v", v.Level)
		}
	})
	t.Run("kernel", func(t *testing.T) {
		if v := Judge(&Info{Proc: Process{PID: 0, Name: "kernel_task"}}); v.Level != LevelSystem {
			t.Errorf("level = %v", v.Level)
		}
	})
}
