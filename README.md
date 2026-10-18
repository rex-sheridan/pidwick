# Pidwick

A terminal companion to Activity Monitor. Activity Monitor tells you *what is
running*; Pidwick tells you **what it is, why it's there, who made it, whether
to trust it, and what it's touching**.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

## Run

```sh
go build -o pidwick . && ./pidwick
sudo ./pidwick   # also shows open files/connections of root-owned processes
```

## What it shows for the selected process

| Tab | Contents |
| --- | --- |
| **Overview** | What it is, why it's on your Mac, safe to quit?, vendor and code-signing identity, install location/source (app bundle, pkg, Homebrew), how launchd starts it, a trust check, command line, family tree |
| **Files** | Working directory, open files, devices, pipes/sockets, third-party code loaded |
| **Network** | Listening ports (and whether they're reachable beyond localhost), live connections with reverse-DNS names |

The **trust check** is a transparent heuristic (code signature, Gatekeeper /
notarization, location, impersonation of Apple process names, persistence,
listening sockets). It is not an antivirus; each finding explains itself.

## Keys

`↑↓`/`jk` select · `tab`/`1-3` switch view · `J`/`K` scroll detail · `/` filter ·
`s` sort · `a` hide Apple/system processes · `r` rescan selected · `f` reveal in
Finder · `c` copy path · `w` web search · `q` quit

## How it gathers data

`ps`, `lsof`, `codesign`, `spctl`, `pkgutil`, `launchctl`, launchd plists,
app `Info.plist`s and a built-in knowledge base of well-known macOS processes
(`internal/proc/kb.json`; contributions welcome).
