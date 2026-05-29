package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/erick303/spacestation/internal/scan"
)

// previewableExts is the allowlist of extensions we hand to Quick Look. It's a
// superset of scan.screenshotExts (so every screenshot qualifies) plus the
// common doc/text/code/media types `qlmanage` renders reliably. Anything not
// listed gets the "not supported" snackbar rather than a blank Quick Look icon.
var previewableExts = map[string]bool{
	// images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".heic": true,
	".heif": true, ".tiff": true, ".bmp": true, ".webp": true, ".icns": true,
	// docs
	".pdf": true, ".rtf": true,
	// text / code
	".txt": true, ".md": true, ".json": true, ".yaml": true, ".yml": true,
	".xml": true, ".csv": true, ".log": true, ".go": true, ".js": true,
	".ts": true, ".sh": true, ".html": true,
	// media
	".mp4": true, ".mov": true, ".m4v": true, ".mp3": true, ".wav": true,
	".aiff": true,
}

// previewClosedMsg is delivered when the Quick Look subprocess exits (the user
// closed the panel). It's a no-op for the model — Quick Look owns its own
// window, so there's nothing to update.
type previewClosedMsg struct{}

// previewRejection reports whether a candidate can be previewed based on its
// fields alone (Action, Path, extension). It performs no IO, so it's cheap and
// unit-testable; directory and existence checks live in the key handler where
// the stat happens. When rejected, msg is a user-facing snackbar string.
func previewRejection(c scan.Candidate) (msg string, rejected bool) {
	if c.Action == scan.ActionCommand || c.Path == "" {
		return "Nothing to preview here.", true
	}
	ext := strings.ToLower(filepath.Ext(c.Path))
	if !previewableExts[ext] {
		if ext == "" {
			return "Preview not supported for this file type.", true
		}
		return fmt.Sprintf("Preview not supported for %s files.", ext), true
	}
	return "", false
}
