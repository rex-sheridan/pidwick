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

## Mouse and keyboard

- **Click** a process to select it; click a tab to switch; click the filter row to search; wheel scrolls.
- **Select text** in the detail pane by dragging; **double-click** selects a word
  (paths and IDs stay whole), **triple-click** a line. Releasing copies to the clipboard.
  Pidwick implements this itself because terminals stop selecting once an app captures the mouse.
  `m` releases the mouse so your terminal's own selection works (press `m` again to restore clicking).
- `y` copies the whole current view.
- **Kill** with `x`: a confirmation offers `t` terminate (SIGTERM), `k` force kill (SIGKILL), or `a`
  terminate the process and all its descendants. It warns about system components, launchd
  auto-restart, other users' processes and child processes, and refuses PID 1 and Pidwick itself.

`↑↓`/`jk` select · `tab`/`1-3` view · `J`/`K` scroll · `/` filter · `s` sort · `a` hide Apple/system ·
`r` rescan selected · `f` reveal in Finder · `c` copy path · `w` web search · `q` quit

## How it gathers data

`ps`, `lsof`, `codesign`, `spctl`, `pkgutil`, `launchctl`, launchd plists,
app `Info.plist`s and a built-in knowledge base of well-known macOS processes
(`internal/proc/kb/*.json`, ~430 entries plus pattern rules for widgets, App Intents, Storage and DriverKit processes; entries marked `inferred` are deduced from the name and framework rather than documented).
