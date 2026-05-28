package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/erick303/spacestation/internal/config"
	"github.com/erick303/spacestation/internal/scan"
	"github.com/erick303/spacestation/internal/tui"
)

func main() {
	var (
		jsonOut     = flag.Bool("json", false, "non-interactive: print candidates as JSON and exit")
		dryRun      = flag.Bool("dry-run", false, "with --json, print what would be deleted (default-selected only)")
		hardDelete  = flag.Bool("hard", false, "permanently delete instead of moving to Trash")
		noDownloads = flag.Bool("no-downloads", false, "skip ~/Downloads")
		noTrash     = flag.Bool("no-trash", false, "skip ~/.Trash")
		showConfig  = flag.Bool("config", false, "print effective config path and exit")
		showVersion = flag.Bool("version", false, "print version and exit")
		scanRoot    rootFlag
	)
	flag.Var(&scanRoot, "scan-root", "additional root to scan (repeatable; overrides config when set)")
	flag.Parse()

	if *showVersion {
		fmt.Println(versionString())
		return
	}

	cfg, cfgPath, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load config (%v); using defaults\n", err)
	}

	if *showConfig {
		fmt.Println(cfgPath)
		return
	}

	if len(scanRoot) > 0 {
		cfg.Scan.ProjectRoots = []string(scanRoot)
	}
	if *noDownloads {
		cfg.Scan.IncludeDownloads = false
	}
	if *noTrash {
		cfg.Scan.IncludeTrash = false
	}

	if *jsonOut {
		runJSON(cfg, *hardDelete, *dryRun)
		return
	}

	if err := tui.Run(cfg, *hardDelete); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type rootFlag []string

func (r *rootFlag) String() string { return strings.Join(*r, ",") }
func (r *rootFlag) Set(s string) error {
	*r = append(*r, s)
	return nil
}

func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "spacestation dev"
	}

	version := info.Main.Version
	// "(devel)", empty, or a v0.0.0-<ts>-<sha> pseudo-version all mean "no
	// tagged release" — collapse to "dev" and let the VCS suffix carry detail.
	if version == "" || version == "(devel)" || strings.HasPrefix(version, "v0.0.0-") {
		version = "dev"
	}

	var revision, date string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			date = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}

	// Build the "(short-sha[-dirty], date)" suffix when VCS info is present.
	var detail string
	if revision != "" {
		short := revision
		if len(short) > 7 {
			short = short[:7]
		}
		if dirty {
			short += "-dirty"
		}
		if date != "" {
			detail = fmt.Sprintf(" (%s, %s)", short, date)
		} else {
			detail = fmt.Sprintf(" (%s)", short)
		}
	}

	return "spacestation " + version + detail
}

func runJSON(cfg config.Config, hard, dry bool) {
	cands := scan.Run(context.Background(), scan.Options{Cfg: cfg}, nil)
	_ = scan.SaveSizeCache()

	if dry {
		out := struct {
			Candidates []scan.Candidate `json:"candidates"`
			Selected   int              `json:"selected_count"`
			Total      int              `json:"total_count"`
			Reclaim    int64            `json:"reclaim_bytes_if_applied"`
		}{Candidates: cands, Total: len(cands)}
		for _, c := range cands {
			if c.Selected {
				out.Selected++
				out.Reclaim += c.SizeBytes
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(out)
		return
	}

	// Non-dry --json: print full report. We don't auto-delete in --json mode.
	out := struct {
		Candidates []scan.Candidate `json:"candidates"`
		HardMode   bool             `json:"hard_mode"`
		Note       string           `json:"note"`
	}{Candidates: cands, HardMode: hard,
		Note: "JSON mode never deletes. Run interactively or pass --dry-run for what-would-be-selected.",
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
