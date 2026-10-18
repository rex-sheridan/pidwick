# Regenerate with: vhs demo.tape   (needs vhs, ttyd, ffmpeg; run from the repo root)
Output docs/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1500
Set Height 860
Set Padding 14
Set Framerate 24
Set TypingSpeed 60ms
Set Theme "Catppuccin Mocha"

# Set up quietly so the first frames aren't a live process list.
Hide
Type "PS1='$ ' && clear" Enter
Type "./pidwick" Enter
Sleep 2s
Type "/"
Type "Finder"
Enter
Sleep 3s
Show

# Overview: what it is, why it's here, who made it, is it safe
Sleep 3s
Type "J"
Sleep 1.5s
Type "J"
Sleep 2s
Type "J"
Sleep 2s

# What it has open
Type "2"
Sleep 3s

# A third-party app: signed, notarized, and its network activity
Type "/"
Ctrl+U
Type "Dropbox.app/Contents/MacOS/Dropbox"
Enter
Sleep 3s
Type "1"
Sleep 3s
Type "3"
Sleep 3s

# Terminating is guarded and explains the consequences
Type "x"
Sleep 3s
Escape
Sleep 1s

Type "q"
Sleep 500ms
