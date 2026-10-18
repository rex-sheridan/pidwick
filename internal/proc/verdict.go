package proc

import (
	"os"
	"path/filepath"
	"strings"
)

type Level int

const (
	LevelUnknown Level = iota
	LevelSystem
	LevelTrusted
	LevelUnverified
	LevelSuspicious
)

func (l Level) String() string {
	switch l {
	case LevelSystem:
		return "APPLE / SYSTEM"
	case LevelTrusted:
		return "SIGNED & TRUSTED"
	case LevelUnverified:
		return "UNVERIFIED"
	case LevelSuspicious:
		return "LOOKS SUSPICIOUS"
	}
	return "UNKNOWN"
}

type Mark int

const (
	MarkGood Mark = iota
	MarkInfo
	MarkWarn
	MarkBad
)

type Finding struct {
	Mark Mark
	Text string
}

type Verdict struct {
	Level    Level
	Headline string
	Findings []Finding
}

// Judge turns the gathered facts into a heuristic verdict. It is not malware
// detection: it only highlights signals a human should look at.
func Judge(in *Info) Verdict {
	v := Verdict{}
	add := func(m Mark, t string) { v.Findings = append(v.Findings, Finding{m, t}) }
	p := in.Proc

	if p.PID == 0 {
		v.Level = LevelSystem
		v.Headline = "Part of the macOS kernel."
		return v
	}
	if p.Path == "" {
		v.Level = LevelUnverified
		v.Headline = "Pidwick couldn't determine which file this process runs."
		add(MarkWarn, "The executable path is hidden (usually because the process belongs to another user). Run `sudo pidwick` to see more.")
		return v
	}
	home, _ := os.UserHomeDir()
	sign := in.Sign
	apple := sign.Kind == SignerApple
	trusted := false

	if !in.Exists {
		add(MarkBad, "The executable file is gone from disk. A running program whose file was deleted or replaced can indicate malware cleaning up after itself (or just an app that updated itself while running).")
	}

	switch {
	case !sign.Signed:
		add(MarkWarn, "Not code-signed. Legitimate software from established vendors is almost always signed.")
	case sign.VerifyErr != "" && sign.GKAccepted:
		add(MarkWarn, "The code signature reports a problem ("+sign.VerifyErr+") although Gatekeeper still accepts the app. Often caused by the app modifying itself or its folder after install.")
		trusted = sign.Kind == SignerDeveloperID || sign.Kind == SignerAppStore
	case sign.VerifyErr != "":
		add(MarkBad, "Signature does not verify: "+sign.VerifyErr+". The file may have been modified after it was signed.")
	case apple:
		add(MarkGood, "Signed by Apple and the signature verifies.")
	case sign.Kind == SignerAdhoc:
		add(MarkWarn, "Ad-hoc signed: it carries a checksum but no developer identity, so nobody vouches for who made it. This is normal for Homebrew packages and software you compiled yourself.")
	case sign.Kind == SignerDeveloperID:
		add(MarkGood, "Signed by developer \""+sign.Signer+"\" (Team ID "+sign.TeamID+") and the signature verifies.")
		trusted = true
	case sign.Kind == SignerAppStore:
		add(MarkGood, "Distributed through the Mac App Store and the signature verifies.")
		trusted = true
	case sign.Kind == SignerDevelopment:
		add(MarkWarn, "Signed with a developer's personal development certificate"+signerSuffix(sign)+", not a distribution certificate. Expected for software someone is building locally.")
	default:
		add(MarkWarn, "Signed with an unrecognised certificate chain"+signerSuffix(sign)+".")
	}

	if sign.Signed && sign.GKKnown {
		switch {
		case sign.GKAccepted && strings.Contains(sign.Gatekeeper, "Notarized"):
			add(MarkGood, "Gatekeeper accepts it and Apple has notarized it (Apple scanned this software for malware).")
		case sign.GKAccepted:
			add(MarkInfo, "Gatekeeper accepts it ("+sign.Gatekeeper+").")
		case !apple:
			add(MarkWarn, "Gatekeeper would reject it: "+nonEmpty(sign.Gatekeeper, "not notarized or not trusted")+".")
			trusted = false
		}
	}

	// location signals
	switch p.Kind {
	case KindTemp:
		add(MarkBad, "Running from a temporary directory ("+filepath.Dir(p.Path)+"). Legitimate software rarely runs from here, whereas droppers and downloaded payloads often do.")
	case KindUser:
		if hiddenDir(p.Path, home) {
			add(MarkWarn, "Running from a hidden folder inside your home directory. Some legitimate tools do this, but so does malware.")
		} else {
			add(MarkInfo, "Lives in your home folder rather than a system-wide location.")
		}
	}

	// impersonation: wears an Apple name but isn't Apple's
	if IsKnownApple(p.Name) && !apple && !trusted && p.Kind != KindSystem && p.Kind != KindBrew {
		add(MarkBad, "Its name matches a well-known Apple process (\""+p.Name+"\") but it is not Apple-signed and does not live in a macOS system folder. Impersonating system process names is a common malware trick.")
	}
	if apple && p.Kind != KindSystem && p.Kind != KindApp {
		add(MarkInfo, "Apple-signed, but running from "+p.Kind.String()+".")
	}

	// persistence
	if in.Job != nil && !in.Job.AppleSigned && (in.Job.RunAtLoad || in.Job.KeepAlive) {
		where := "launchd"
		if strings.HasPrefix(in.Job.PlistPath, home+"/") {
			where = "your personal LaunchAgents folder"
		}
		add(MarkInfo, "Set up to start automatically via "+where+" ("+in.Job.PlistPath+").")
	}

	// listening sockets
	for _, c := range in.Handles.Conns {
		if c.State == "LISTEN" && c.Scope == "any" && !apple && !trusted {
			add(MarkWarn, "Unverified program is listening for network connections on "+c.Local+" (all interfaces).")
			break
		}
	}

	switch {
	case !in.Exists || hasMark(v.Findings, MarkBad):
		v.Level = LevelSuspicious
	case apple:
		v.Level = LevelSystem
	case trusted:
		v.Level = LevelTrusted
	default:
		v.Level = LevelUnverified
	}
	// unsigned or ad-hoc code in a temp directory is the classic dropper pattern
	if v.Level == LevelUnverified && (!sign.Signed || sign.Kind == SignerAdhoc) && p.Kind == KindTemp {
		v.Level = LevelSuspicious
	}

	switch v.Level {
	case LevelSystem:
		v.Headline = "Apple-made operating system component."
	case LevelTrusted:
		who := sign.Signer
		if sign.Kind == SignerAppStore {
			who = "the Mac App Store"
		}
		v.Headline = "Signed software from " + strings.TrimSuffix(nonEmpty(who, "an identified developer"), ".") + "."
	case LevelUnverified:
		v.Headline = "Can't confirm who made this. Not necessarily bad; see why below."
	case LevelSuspicious:
		v.Headline = "Several signals deserve a closer look. Not proof of malware."
	}
	return v
}

func signerSuffix(s Signature) string {
	if s.Signer != "" {
		return " (" + s.Signer + ")"
	}
	return ""
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func hasMark(fs []Finding, m Mark) bool {
	for _, f := range fs {
		if f.Mark == m {
			return true
		}
	}
	return false
}

func hiddenDir(path, home string) bool {
	rel := path
	if home != "" {
		rel = strings.TrimPrefix(path, home+"/")
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts[:max(0, len(parts)-1)] {
		if strings.HasPrefix(p, ".") && p != "." && p != ".." {
			return true
		}
	}
	return false
}
