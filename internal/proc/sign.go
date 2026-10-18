package proc

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"howett.net/plist"
)

// SignerKind says who vouches for a binary.
type SignerKind int

const (
	SignerNone SignerKind = iota
	SignerApple
	SignerAppStore
	SignerDeveloperID
	SignerDevelopment
	SignerAdhoc
	SignerOther
)

func (s SignerKind) String() string {
	switch s {
	case SignerApple:
		return "Apple"
	case SignerAppStore:
		return "Mac App Store"
	case SignerDeveloperID:
		return "Developer ID"
	case SignerDevelopment:
		return "Development certificate"
	case SignerAdhoc:
		return "Ad-hoc (no identity)"
	case SignerOther:
		return "Other certificate"
	}
	return "Unsigned"
}

type Signature struct {
	Signed      bool
	Kind        SignerKind
	Identifier  string
	Authorities []string
	TeamID      string
	Signer      string // human-readable developer name
	Hardened    bool
	Platform    bool // Apple platform binary
	Verified    bool
	VerifyErr   string
	Gatekeeper  string // "Notarized Developer ID", "Apple System", ...
	GKAccepted  bool
	GKKnown     bool
}

var signerRe = regexp.MustCompile(`^(?:Developer ID Application|Apple Development|Apple Distribution|Mac Developer|3rd Party Mac Developer Application|Developer ID Installer): (.*?)(?: \(([A-Z0-9]{10})\))?$`)

func parseCodesign(out string) Signature {
	var s Signature
	if strings.Contains(out, "not signed at all") {
		return s
	}
	s.Signed = true
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "CodeDirectory ") && (strings.Contains(line, "(runtime") || strings.Contains(line, ",runtime")) {
			s.Hardened = true
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "Identifier":
			s.Identifier = v
		case "Authority":
			s.Authorities = append(s.Authorities, v)
		case "TeamIdentifier":
			if v != "not set" {
				s.TeamID = v
			}
		case "Signature":
			if v == "adhoc" {
				s.Kind = SignerAdhoc
			}
		case "Platform identifier":
			s.Platform = true
		}
	}
	if s.Platform {
		s.Kind = SignerApple
		s.Signer = "Apple"
	}
	if len(s.Authorities) > 0 && s.Kind != SignerAdhoc && !s.Platform {
		a := s.Authorities[0]
		switch {
		case strings.HasSuffix(a, "Software Signing"):
			s.Kind, s.Signer = SignerApple, "Apple"
		case strings.HasPrefix(a, "Developer ID Application"):
			s.Kind = SignerDeveloperID
		case a == "Apple Mac OS Application Signing", strings.HasPrefix(a, "3rd Party Mac Developer Application"):
			s.Kind, s.Signer = SignerAppStore, "Mac App Store developer"
		case strings.HasPrefix(a, "Apple Development"), strings.HasPrefix(a, "Mac Developer"), strings.HasPrefix(a, "Apple Distribution"):
			s.Kind = SignerDevelopment
		case strings.Contains(a, "Apple") && strings.Contains(a, "Signing"):
			s.Kind, s.Signer = SignerApple, "Apple"
		default:
			s.Kind = SignerOther
		}
		if m := signerRe.FindStringSubmatch(a); m != nil {
			s.Signer = m[1]
			if s.TeamID == "" {
				s.TeamID = m[2]
			}
		}
		if s.Kind == SignerOther && s.Signer == "" {
			s.Signer = a
		}
	}
	return s
}

func run(ctx context.Context, d time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// inspectSignature gathers code-signing and Gatekeeper facts for a binary.
// target is the outermost .app if there is one, otherwise the executable.
func inspectSignature(ctx context.Context, exe, target string) Signature {
	out, _ := run(ctx, 6*time.Second, "codesign", "-dv", "--verbose=4", exe)
	s := parseCodesign(out)
	if !s.Signed {
		return s
	}
	vout, err := run(ctx, 8*time.Second, "codesign", "--verify", "--strict", target)
	if err != nil && strings.Contains(vout, "detritus") {
		// Stray Finder metadata (xattrs) trips --strict but doesn't mean tampering.
		vout, err = run(ctx, 8*time.Second, "codesign", "--verify", target)
	}
	if err != nil {
		s.VerifyErr = firstLine(vout)
	} else {
		s.Verified = true
	}
	gk, _ := run(ctx, 8*time.Second, "spctl", "-a", "-vv", "-t", "exec", target)
	switch {
	case strings.Contains(gk, "does not seem to be an app"), strings.Contains(gk, "internal error"):
		// Gatekeeper only assesses app bundles; plain tools are inconclusive.
	case strings.Contains(gk, ": accepted"):
		s.GKKnown, s.GKAccepted = true, true
	case strings.Contains(gk, ": rejected"):
		s.GKKnown = true
	}
	for _, l := range strings.Split(gk, "\n") {
		if v, ok := strings.CutPrefix(l, "source="); ok && s.GKKnown {
			s.Gatekeeper = v
		}
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Bundle describes the .app (or other bundle) an executable belongs to.
type Bundle struct {
	Path       string
	Name       string
	Identifier string
	Version    string
	Copyright  string
	Outermost  string // top-level .app containing Path
	AppStore   bool
}

func readBundle(exe string) *Bundle {
	idx := strings.Index(exe, ".app/")
	if idx < 0 {
		return nil
	}
	outer := exe[:idx+4]
	inner := exe[:strings.LastIndex(exe, ".app/")+4]
	b := &Bundle{Path: inner, Outermost: outer}
	b.AppStore = fileExists(outer + "/Contents/_MASReceipt")
	data, err := readFile(inner + "/Contents/Info.plist")
	if err != nil {
		return b
	}
	var info map[string]any
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return b
	}
	str := func(k string) string { s, _ := info[k].(string); return s }
	b.Name = str("CFBundleDisplayName")
	if b.Name == "" {
		b.Name = str("CFBundleName")
	}
	b.Identifier = str("CFBundleIdentifier")
	b.Version = str("CFBundleShortVersionString")
	if v := str("CFBundleVersion"); v != "" && v != b.Version {
		if b.Version == "" {
			b.Version = v
		} else {
			b.Version += " (" + v + ")"
		}
	}
	b.Copyright = str("NSHumanReadableCopyright")
	return b
}
