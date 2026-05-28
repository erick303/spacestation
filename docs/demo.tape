# VHS tape — records the spacestation TUI as a GIF + still PNG.
#
# Requires charmbracelet/vhs (`brew install vhs ttyd ffmpeg`) and the
# spacestation binary on $PATH (`go install .` from the repo root).
#
# The demo is recorded against a sanitized fixture tree under
# /tmp/spacestation-demo (built by docs/setup-fixtures.sh), so the GIF
# doesn't leak real project names from ~/projects.
#
# Regenerate:
#   vhs docs/demo.tape

Output docs/demo.gif

Set FontSize       14
Set Width          1400
Set Height         820
Set Theme          "TokyoNight"
Set Padding        24
Set TypingSpeed    40ms

# Build the sanitized fixture tree off-camera.
Hide
Type "bash docs/setup-fixtures.sh"
Enter
Sleep 1s
Show

# Launch against the fixture root.
Type "spacestation --scan-root /tmp/spacestation-demo"
Enter

# Wait for the scan to finish. Bump this if your cold scan takes longer than
# the warm scan — the tape needs the TUI to settle into the browsing screen
# before the screenshot is captured.
Sleep 8s

# Capture the hero still: dashboard visible, first group expanded.
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

# Back up to show the disk dashboard area lingering.
Type "["
Type "["
Sleep 1s

# Quit cleanly.
Type "q"
Sleep 500ms
