package scan

import (
	"encoding/json"
	"time"
)

type Category int

const (
	CatNodeModules Category = iota
	CatJSBuild
	CatPython
	CatRust
	CatJVM
	CatGoCache
	CatXcode
	CatHomebrew
	CatDocker
	CatSystemCache
	CatDownloads
	CatTrash
	CatOther
)

// categoryMeta holds per-category metadata that the rest of the codebase
// reads through Category.String() / Category.SortOrder(). Single source of
// truth — adding a category is one new iota const + one new row here.
// Out-of-range Categories degrade to "Other" / 99.
var categoryMeta = [...]struct {
	name      string // display name, also the value emitted in --json
	sortOrder int    // lower = earlier in the UI
}{
	CatNodeModules: {"Node.js", 0},
	CatJSBuild:     {"JS Build Output", 1},
	CatPython:      {"Python", 2},
	CatRust:        {"Rust", 3},
	CatJVM:         {"JVM/Gradle", 4},
	CatXcode:       {"Xcode", 5},
	CatGoCache:     {"Go Cache", 6},
	CatDocker:      {"Docker", 7},
	CatHomebrew:    {"Homebrew", 8},
	CatSystemCache: {"System Cache", 9},
	CatDownloads:   {"Downloads", 10},
	CatTrash:       {"Trash", 11},
	CatOther:       {"Other", 99},
}

func (c Category) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

func (c Category) String() string {
	if int(c) < 0 || int(c) >= len(categoryMeta) {
		return "Other"
	}
	return categoryMeta[c].name
}

// SortOrder is the display rank for grouping in the UI (lower = earlier).
func (c Category) SortOrder() int {
	if int(c) < 0 || int(c) >= len(categoryMeta) {
		return 99
	}
	return categoryMeta[c].sortOrder
}

type Safety int

const (
	SafetyUnknown Safety = iota
	SafetyRegenerable
	SafetyUserContent
)

// Action is how a candidate gets cleaned up.
type Action int

const (
	ActionDelete  Action = iota // remove Path (Trash or hard, per cfg)
	ActionCommand               // run the ecosystem's own cleanup command
)

func (a Action) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

func (a Action) String() string {
	switch a {
	case ActionCommand:
		return "command"
	default:
		return "delete"
	}
}

func (s Safety) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s Safety) String() string {
	switch s {
	case SafetyRegenerable:
		return "regenerable"
	case SafetyUserContent:
		return "user content"
	default:
		return "unknown"
	}
}

type Candidate struct {
	Path        string    `json:"path"`
	Title       string    `json:"title,omitempty"` // human-friendly label; falls back to Path
	Category    Category  `json:"category"`
	SizeBytes   int64     `json:"size_bytes"`
	LastTouched time.Time `json:"last_touched"`
	Safety      Safety    `json:"safety"`
	Selected    bool      `json:"selected"`
	Reason      string    `json:"reason"`
	Detail      string    `json:"detail"`

	Action  Action   `json:"action"`
	Command []string `json:"command,omitempty"` // when Action == ActionCommand
}

// DisplayTitle returns Title if set, else Path.
func (c Candidate) DisplayTitle() string {
	if c.Title != "" {
		return c.Title
	}
	return c.Path
}
