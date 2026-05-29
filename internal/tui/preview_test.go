package tui

import (
	"testing"

	"github.com/erick303/spacestation/internal/scan"
)

func TestPreviewRejection(t *testing.T) {
	tests := []struct {
		name     string
		cand     scan.Candidate
		rejected bool
		wantMsg  string
	}{
		{
			name:     "screenshot png is previewable",
			cand:     scan.Candidate{Path: "/Users/x/Desktop/Screenshot 2026-01-12 at 09.43.30.png", Category: scan.CatScreenshots},
			rejected: false,
		},
		{
			name:     "pdf download is previewable",
			cand:     scan.Candidate{Path: "/Users/x/Downloads/report.pdf", Category: scan.CatDownloads},
			rejected: false,
		},
		{
			name:     "unsupported extension is rejected with ext message",
			cand:     scan.Candidate{Path: "/Users/x/Downloads/db.sqlite"},
			rejected: true,
			wantMsg:  "Preview not supported for .sqlite files.",
		},
		{
			name:     "no extension is rejected",
			cand:     scan.Candidate{Path: "/Users/x/Downloads/Makefile"},
			rejected: true,
			wantMsg:  "Preview not supported for this file type.",
		},
		{
			name:     "command-action candidate is rejected",
			cand:     scan.Candidate{Path: "", Action: scan.ActionCommand, Command: []string{"docker", "system", "prune"}},
			rejected: true,
			wantMsg:  "Nothing to preview here.",
		},
		{
			name:     "empty path is rejected",
			cand:     scan.Candidate{Path: ""},
			rejected: true,
			wantMsg:  "Nothing to preview here.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, rejected := previewRejection(tt.cand)
			if rejected != tt.rejected {
				t.Fatalf("rejected = %v, want %v (msg=%q)", rejected, tt.rejected, msg)
			}
			if rejected && msg != tt.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tt.wantMsg)
			}
			if !rejected && msg != "" {
				t.Errorf("msg = %q, want empty for an accepted candidate", msg)
			}
		})
	}
}
