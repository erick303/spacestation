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

func (c Category) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.String())
}

func (c Category) String() string {
	switch c {
	case CatNodeModules:
		return "Node.js"
	case CatJSBuild:
		return "JS Build Output"
	case CatPython:
		return "Python"
	case CatRust:
		return "Rust"
	case CatJVM:
		return "JVM/Gradle"
	case CatGoCache:
		return "Go Cache"
	case CatXcode:
		return "Xcode"
	case CatHomebrew:
		return "Homebrew"
	case CatDocker:
		return "Docker"
	case CatSystemCache:
		return "System Cache"
	case CatDownloads:
		return "Downloads"
	case CatTrash:
		return "Trash"
	default:
		return "Other"
	}
}

// SortOrder is used to order groups in the UI (lower = earlier).
func (c Category) SortOrder() int {
	switch c {
	case CatNodeModules:
		return 0
	case CatJSBuild:
		return 1
	case CatPython:
		return 2
	case CatRust:
		return 3
	case CatJVM:
		return 4
	case CatXcode:
		return 5
	case CatGoCache:
		return 6
	case CatDocker:
		return 7
	case CatHomebrew:
		return 8
	case CatSystemCache:
		return 9
	case CatDownloads:
		return 10
	case CatTrash:
		return 11
	default:
		return 99
	}
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
