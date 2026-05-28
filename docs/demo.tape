# VHS tape — records the spacestation TUI as a GIF + PNG.
#
# Requires charmbracelet/vhs (`brew install vhs`) and the spacestation binary
# on $PATH (`go install .` from the repo root).
#
# Regenerate:
#   vhs docs/demo.tape
#
# Tip: to avoid recording real project names from ~/projects in the demo,
# point at a sanitized scan root, e.g. create a fixtures dir and pass
# `--scan-root /tmp/spacestation-demo` here.

Output docs/demo.gif
Output docs/hero.png

Set FontSize       14
Set Width          1400
Set Height         820
Set Theme          "Tokyo Night"
Set Padding        24
Set TypingSpeed    40ms
Set PlaybackSpeed  1.0

# Launch.
Type "spacestation"
Enter

# Wait for the scan to finish. Bump this if your cold scan takes longer than
# the warm scan — the tape needs the TUI to settle into the browsing screen
# before the screenshot is captured.
Sleep 8s

# Capture the hero frame: dashboard visible, first group expanded.
Screenshot docs/hero.png

# Walk down through a few rows so the GIF shows the cursor moving.
Down@250ms 4
Sleep 600ms

# Jump to the next group with the vim-style binding so the navigation is visible.
Type "]"
Sleep 600ms
Type "]"
Sleep 800ms

# Toggle the current item.
Space
Sleep 800ms

# Back up and show the disk dashboard area lingering.
Type "[" Type "["
Sleep 1s

# Quit cleanly.
Type "q"
Sleep 500ms
