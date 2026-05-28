package scan

import (
	"strings"
	"testing"
	"time"

	"github.com/erick303/spacestation/internal/config"
)

func TestApplyScoring(t *testing.T) {
	now := time.Now()

	cfg := config.Default()
	cfg.Selection.DefaultSelectMinAgeDays = 30
	cfg.Selection.DownloadsMinAgeDays = 90

	cases := []struct {
		name           string
		in             Candidate
		wantSelected   bool
		wantReasonHas  string
		wantReasonMiss string // substring that MUST NOT appear (e.g. "-")
	}{
		{
			name: "zero mtime regenerable: do not auto-select",
			in: Candidate{
				Category: CatNodeModules,
				Safety:   SafetyRegenerable,
				// LastTouched left as time.Time{}
			},
			wantSelected:  false,
			wantReasonHas: "unknown age",
		},
		{
			name: "zero mtime Trash: still auto-selects",
			in: Candidate{
				Category: CatTrash,
				Safety:   SafetyUserContent,
			},
			wantSelected:  true,
			wantReasonHas: "Trash",
		},
		{
			name: "future mtime regenerable: not selected, reason clamps to 0d (no negative)",
			in: Candidate{
				Category:    CatNodeModules,
				Safety:      SafetyRegenerable,
				LastTouched: now.Add(5 * 24 * time.Hour),
			},
			wantSelected:   false,
			wantReasonHas:  "0d",
			wantReasonMiss: "-",
		},
		{
			name: "stale regenerable (60d): auto-selects",
			in: Candidate{
				Category:    CatNodeModules,
				Safety:      SafetyRegenerable,
				LastTouched: now.Add(-60 * 24 * time.Hour),
			},
			wantSelected:  true,
			wantReasonHas: "Stale 60d",
		},
		{
			name: "recent regenerable (5d): not auto-selected",
			in: Candidate{
				Category:    CatNodeModules,
				Safety:      SafetyRegenerable,
				LastTouched: now.Add(-5 * 24 * time.Hour),
			},
			wantSelected:  false,
			wantReasonHas: "Active",
		},
		{
			name: "old download (120d): auto-selects",
			in: Candidate{
				Category:    CatDownloads,
				Safety:      SafetyUserContent,
				LastTouched: now.Add(-120 * 24 * time.Hour),
			},
			wantSelected:  true,
			wantReasonHas: "Untouched",
		},
		{
			name: "recent download (10d): not auto-selected",
			in: Candidate{
				Category:    CatDownloads,
				Safety:      SafetyUserContent,
				LastTouched: now.Add(-10 * 24 * time.Hour),
			},
			wantSelected:  false,
			wantReasonHas: "Recent",
		},
		{
			name: "Trash always selects",
			in: Candidate{
				Category:    CatTrash,
				Safety:      SafetyUserContent,
				LastTouched: now.Add(-1 * time.Hour),
			},
			wantSelected:  true,
			wantReasonHas: "Trash",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cands := []Candidate{tc.in}
			applyScoring(cands, cfg)
			got := cands[0]
			if got.Selected != tc.wantSelected {
				t.Errorf("Selected = %v, want %v (reason: %q)", got.Selected, tc.wantSelected, got.Reason)
			}
			if !strings.Contains(got.Reason, tc.wantReasonHas) {
				t.Errorf("Reason = %q, want substring %q", got.Reason, tc.wantReasonHas)
			}
			if tc.wantReasonMiss != "" && strings.Contains(got.Reason, tc.wantReasonMiss) {
				t.Errorf("Reason = %q, must NOT contain %q", got.Reason, tc.wantReasonMiss)
			}
		})
	}
}
