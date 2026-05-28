# spacestation — review findings

Curated from a multi-axis review (correctness, architecture, TUI/UX, code quality, hygiene). Each finding below was re-verified against the actual code. Findings that were duplicate, false-positive, or already addressed have been dropped. Where the original review overstated severity, this document downgrades it with a note.

Status snapshot at time of review:
- `go build ./...`, `go vet ./...` clean.
- Only `internal/scan/scan_test.go` has tests.
- LICENSE, `.gitignore`, `.github/workflows/ci.yml`, `docs/demo.tape` are already present.

---

## CRITICAL — destructive-action safety

_(none open — see Resolved section at bottom)_

---

## HIGH — correctness bugs

_(none open — see Resolved section at bottom)_

---

## LOW — UX & polish

### L2. No `?` help / no `/` filter / no `o` open-in-Finder
**File:** keybindings in `internal/tui/model.go:245-305`
**Verification:** confirmed. Two-line help at the bottom covers the basics; for a 500+ row list with 14 bindings the conventions are a `?` modal, `/` filter, and `o` reveal. The list also has no scroll-position indicator.

**Fix:** at minimum a `?` overlay. Filter and reveal are larger items but would change the product feel.

---

## HYGIENE — remaining gaps

Most hygiene items the original review flagged are already in place (LICENSE, .gitignore, CI workflow, docs/demo.tape).

### Hy1. No git remote configured and no tags
**Verification:** `git remote -v` returns empty; `git tag` returns empty.
**Impact:** README's `git clone https://github.com/erick303/spacestation` won't resolve until the repo is pushed; `go install github.com/erick303/spacestation@latest` won't work without at least one tag.
**Fix:** push to `github.com/erick303/spacestation`, tag `v0.1.0`, update README install snippet to lead with `go install ...@latest`.

### Hy4. Test coverage is concentrated in the wrong place
**Verification:** confirmed.
- `internal/scan/scan_test.go` exercises walk classification, `.git` skip, `DirSize` sum, `LastTouched`.
- Zero tests for the destructive surface: `internal/trash/`, `internal/cleanup/`, `internal/score/`, `internal/scan/sizecache.go`.
**Highest-value gaps to plug first:**
- `score.Apply` (pure, table-driven, no FS) — would have caught H6 today.
- AppleScript string generation in `trash.Move` (unit-testable without exec).
- Cleanup mode dispatch in `cleanup.Execute` — would catch any future regression of C1.
- `sizecache` hit/miss/invalidate behavior.

---

## Suggested fix order

1. **C1** — silent Trash→Hard escalation. Half a day.
2. **H1 + H2 + H7** — same root cause (no lifecycle owner for the in-flight async work). Put a cancel func on the model, thread ctx into walkers + cleanup, route `ctrl+c` and `r` through it. A day.
3. **H6** — zero-mtime auto-select; a 2-line guard in `score.Apply`. Trivial.
4. **H4** — rune/width math via `lipgloss.Width` + `runewidth`. Half a day, much of it tedious.
5. **M10** — dead-code purge, ~80 LOC removed. An hour.
6. **M3 + M4 + M7** — collapse the enum/string parallelism and centralize ordering. Half a day; sets up M5 (string-ID categories) if you want to go further.
7. **M1 + M2** — fold `score` into `scan`, `trash` into `cleanup`. Mechanical refactor; net simpler.
8. **Hy1 + Hy2 + Hy4** — push remote + tag + macOS build constraints + fill the highest-value test gaps.

After steps 1–4 the tool is honest about what it does. After 5–8 the codebase is easier to extend and ready to share.

---

## Resolved

### M9. Mockup-vs-delivered: cleaning UI is one spinner
Deferred to post-v0.1.0 as a known limitation, not closed via code. The mockup's per-step progress bar / live command output / per-batch checkmarks is a real product feature, not a finding-sized fix — `cleanup.Execute` would need to stream results via a channel and the TUI would need to subscribe. The current single-spinner UI is honest about what it does and the cancel path (H7) gives the user a way out if a `docker system prune` runs long. Recording the gap rather than closing as Won't Fix because it remains worth doing later.

### M11. `probeBrewSmart` regex recompile + redundant split-concat-split
Won't fix. The "fix" is to lift `sizeRe` to package scope and rename `parseDockerSize` so it takes pre-split `(num, unit)` arguments. Real cost is one `regexp.Compile` per `brew cleanup --dry-run` call (which itself takes seconds), and a string concat that's immediately re-split — measurably zero. Closing as a reasoned waiver to avoid touching working parser code for negligible benefit; the ugliness is local and not on any hot path.

### M17. AppleScript path escaping uses `%q`, not AppleScript escaping
Won't fix. Post-C1, the failure mode for an unrepresentable filename is the right one: `osascript` errors, the per-path error surfaces in the done view, the file stays where it was. Go's `%q` and AppleScript string syntax agree on every common escape and every printable Unicode rune; they differ only on `\xNN` (non-printable bytes), which macOS filenames can technically contain but in practice don't. Switching to argv-passing `osascript -e 'on run argv …' arg1 arg2 …` would be defensive hardening for a case with no observable user impact.

### Hy2. No `//go:build darwin` constraints; macOS-only code compiles on Linux
Resolved with a runtime guard rather than build constraints. Verified by cross-compile: the code is source-portable (no actually-Darwin-only syscalls — `Stat_t`/`Statfs_t` field accesses go through portable casts), so build tags wouldn't catch anything at compile time. Added an early `runtime.GOOS != "darwin"` check at the top of `main()` that prints `"spacestation is macOS-only (built for darwin, running on <goos>)"` to stderr and exits 1. Captures the real concern (a user on another platform getting a useless silent binary) without the file-shuffle dance of dual `main_darwin.go` / `main_other.go` stubs that would add no signal.

### M12. Unbounded fan-out under expand-probes
Resolved. `runFixedProbe`'s expand path now caps child concurrency with a `make(chan struct{}, workers)` semaphore. Before: `~/Library/Caches` with N children spawned N goroutines each running `CachedDirSize(ctx, ..., workers)` for a worst case of ~N × workers concurrent walker goroutines (and FDs). Now: at most `workers × workers`, which scales with the worker pool the caller already chose. `defer func() { <-childSem }()` releases the slot even on panic.

### L1. `c` "clear-all" is one key from `ctrl+c`
Resolved. `c` now uses the same two-press arm pattern as space-on-a-group-header: first press flashes "Press c again to clear all N selections" and arms a 3-second window; second press within the window commits. With nothing selected, the arm is skipped and a "Nothing selected to clear." flash fires instead. Added `armedClearAll` + `armedClearAllExpiry` fields on the model and reset them in `resetForRescan` alongside the existing arm state.

### M15. Initial walk goroutine is unnecessary
Resolved. In both `walkProjects` (scan.go) and `DirSize` (size.go) the initial walk was spawned in a goroutine that the caller immediately blocked on via `wg.Wait()` — a no-op detour through a goroutine plus a wasted semaphore slot. Both call sites now do `wg.Add(1); walk(root); wg.Wait()` directly. The recursive bodies still spawn goroutines and take sem slots normally; this only collapses the redundant entrypoint.

### M13. `recency.go` does an extra `Lstat` per entry
Resolved. `LastTouched` now calls `e.Info()` on the `os.DirEntry` instead of doing a fresh `os.Lstat(filepath.Join(root, e.Name()))`. On Unix-like platforms `DirEntry.Info()` returns the stat the directory read already populated, saving one syscall per entry. `path/filepath` import dropped — no longer used.

### L3. README key table missing several bindings
Resolved. README's keys table now covers `g / G` (top/bottom, also `home`/`end`), `[ / ]` (jump to previous/next group header), `pgup`/`pgdn`, `x` (permanent Trash action — remove checked items, or empty whole Trash if none checked), and `v` (toggle dashboard). `q / ctrl+c` are grouped together since both quit.

### L5. README says "Requires Go 1.22+" but go.mod requires 1.25
Resolved. README updated to "Requires Go 1.25+ (matches `go.mod` and CI)". Chose to align the README to reality rather than lower `go.mod` — the project has no portability mandate (CI is pinned to 1.25, single developer on 1.25), so widening the install audience to 1.22 users adds no real value and creates a maintenance question.

### L4. README references missing demo assets
Resolved (verified, no code change). `docs/hero.png` (461 KB), `docs/demo.gif` (498 KB), and `docs/demo.tape` are all present in the repo, so the README image links at lines 9 and 11 render correctly on GitHub. Finding was already addressed by prior demo work — listed here only to keep the audit trail complete.

### M8. TUI knows literal `"hard"` string
Resolved. `main.go` resolves the effective delete mode once from `--hard` + `cfg.Delete.Mode` into a `cleanup.Mode` value and passes it into `tui.Run(cfg, mode)`. The model stores it as `deleteMode cleanup.Mode`; the three former `m.hardDelete || m.cfg.Delete.Mode == "hard"` sites (confirm-view verb, done-view verb, executeClean) now compare `m.deleteMode == cleanup.ModeHard`, and `executeClean`'s local `mode := cleanup.ModeTrash; if … { mode = cleanup.ModeHard }` block collapses to `mode := m.deleteMode`. The `"hard"` string literal no longer leaks past the config-loading boundary.

### C1. Silent Trash→Hard escalation in `cleanup.Execute`
Resolved. `cleanup.Execute` no longer escalates Trash failures to `RemoveAll`. Per-path Move errors now flow through to the done view, which already iterates and prints them (`internal/tui/model.go:805-811`). Trash mode now matches the confirm-hint promise.

### H1. Rescan leaks the previous scan's goroutine
Resolved. `DirSize`, `CachedDirSize`, and the four probe orchestrators (`probeFixedPaths`, `probeSmart`, `probeDownloads`, `probeTrash`) now take a `context.Context` so cancellation actually stops in-flight work. The model owns a `scanCancel` + `scanFinished` pair; rescan handlers capture them before resetting, and `beginScan` waits for the old scan to drain inside its Cmd goroutine before starting the new one. Both `scanProgressMsg` and `scanDoneMsg` carry the originating channel ref so stale messages from a cancelled scan can't pollute the new scan's UI. `TestDirSizeRespectsContext` guards against regression.

### H2. `q` during scanning doesn't cancel the scan goroutine
Resolved (same commit as H1). `stageScanning`'s quit handler now calls `m.scanCancel()` before `tea.Quit`. Walkers observe `ctx.Done()` at their next ReadDir tick (typically a few ms) and unwind, freeing FDs and letting the tail `SaveSizeCache` write complete before the process exits.

### H7. `stageCleaning` swallows `ctrl+c`
Resolved. `cleanup.Execute(ctx, …)`, `runCommand(parent, …)`, `trash.Move(ctx, …)`, `trash.Hard(ctx, …)`, and `emptyTrash(ctx, …)` all take a context now. The TUI's `executeClean` creates the context synchronously, stores `cleanCancel` on the model, and passes it into the Cmd goroutine. `stageCleaning` handles `esc`/`ctrl+c` (cancel and fall through to done view with partial results) and `q` (cancel and quit). `runCommand`'s timeout is derived from the parent ctx, so a cancel kills the in-flight subprocess via `exec.CommandContext`. The view hint advertises `esc cancel · q quit`, and flips to a "cancelling…" warning once the cancel has been issued. Test coverage for cleanup is deferred to Hy4.

### H3. Hardlink double-counting in `DirSize`
Resolved. `DirSize` now maintains a per-call `map[inodeKey]struct{}` (keyed by `(dev, ino)`) shared across walker goroutines under a small mutex. Files with `Nlink > 1` are looked up before their size is added; if the inode was already counted, the file's bytes are skipped. Files with `Nlink == 1` never consult the map, so the common case stays lock-free.

Fixes the worst case (a pnpm `node_modules` where every shared file is hardlinked back to the CAS store reported `~14×` its actual on-disk size). `TestDirSizeDeduplicatesHardlinks` creates a file plus three hardlinks and asserts `DirSize` returns one copy's size, not four.

Cross-candidate dedup (e.g. recognising that <code>node_modules</code> and <code>~/Library/pnpm/store</code> share inodes) is intentionally not done — see `docs/h3-hardlinks-decision.html` for the rationale.

### H4. Pad/truncate use `len()` (bytes), not display width
Resolved. `truncatePath`, `padRight`, and `padLeft` now all live in `internal/tui/format.go` and operate on display width:

- `truncatePath` uses `runewidth` to walk runes from the right within a width budget, never producing invalid UTF-8 or mid-rune cuts.
- `padRight` / `padLeft` measure with `lipgloss.Width(s)`, so ANSI escape sequences in styled inputs don't throw the pad count off.
- `renderItemRow`'s byte-slice hack for smart-probe coloring is gone: we now style the visible text first and pad after.
- `dashboard.go`'s hardcoded `const sepW = 3` is replaced with `sepW := lipgloss.Width(sep)`, and category-entry widths use `lipgloss.Width(txt)` instead of `len(txt)`.

`internal/tui/format_test.go` covers ASCII truncation, multibyte runes (no mid-byte cut), wide runes (CJK respects 2-cell width), ANSI-styled `padRight`, and multibyte `padLeft`. Promoted `github.com/mattn/go-runewidth` from indirect to direct in go.mod.

### H6. `score.Apply` mis-handles zero/future mtime
Resolved. Two guards added at the top of the `score.Apply` loop:

1. `LastTouched.IsZero() && Category != CatTrash` → set reason to `"unknown age — not auto-selecting"` and skip. A directory we couldn't `stat` no longer gets armed for deletion; Trash keeps its always-selectable exception.
2. Future-mtime ages are clamped to `0` so the reason text reads `Active (0d)` instead of `(-5d)`.

Added `internal/score/score_test.go` with 8 table-driven cases (zero mtime regenerable, zero mtime Trash, future mtime, stale regenerable, recent regenerable, old download, recent download, Trash). The test catches the original H6 bug and pins the new rule in place.

### M16. `SaveSizeCache` marshals the map without holding the lock
Resolved (rolled into H1). `SaveSizeCache` now `RLock`s for the full duration of `json.Marshal` rather than releasing before the marshal. Multiple concurrent readers are still allowed; concurrent writers (CachedDirSize cache-misses) wait their turn. Eliminates the "concurrent map iter + write" panic that H1's improved rescan made reliably triggerable.

### H5. Size-cache key ignores inode/device
Resolved. `sizeCacheEntry` now carries `Dev` and `Ino` (omitempty on platforms without `syscall.Stat_t`). `CachedDirSize` records them on write and verifies them on hit alongside the mtime, so a rename-collision or volume swap that happens to leave mtimes equal no longer returns the stale cached size. `TestCachedDirSizeRespectsInode` pre-populates an entry, mutates the in-memory Ino + size, and asserts the next lookup recomputes rather than returning the poisoned value.

### Hy3. No `--version` flag / build-info embedding
Resolved. `main.go` now exposes a `--version` flag that reads `runtime/debug.ReadBuildInfo()` and reports either the module version (for `go install` builds) or the VCS commit + dirty flag (for local builds), falling back to "dev" when neither is available. Zero build-tooling dependency — no `-ldflags`, no `vendor.go` shenanigans.

### H8. `tea.WithMouseCellMotion()` enabled with zero mouse handlers
Resolved. Removed `tea.WithMouseCellMotion()` from `tea.NewProgram` at `internal/tui/model.go:23`. No `tea.MouseMsg` cases exist in Update, so the option was pure cost — terminal emulators (iTerm, Alacritty, etc.) intercept the mouse stream and require Option/Shift to copy text. Native click-drag-to-copy now works again. The option can come back the day a mouse handler is added.

### M6. `smartClaimedPaths()` exists only to dodge dedupe
Resolved with a different fix than the finding proposed. The original suggestion was "drop `smartClaimedPaths` and trust the dedupe pass" — that's wrong. The dedupe in `scan.Run` is *path-keyed*, but three smart probes (brew, docker, xcrun) emit synthetic-URI paths (`brew://cleanup`, `docker://system-prune`, `xcrun://simctl-delete-unavailable`) that don't share a path with the corresponding fixed probe. Drop the pre-filter and both a "delete this dir" and "run this command" candidate land in the list. Verified empirically before fixing.

What actually changed: rather than a separate `smartClaimedPaths()` function listing tool→path mappings, the relationship now lives on the probe itself as a `replacedBy []string` field naming the tools whose presence suppresses this probe. The `probeFixedPaths` orchestrator builds the claimed-path set in one short loop over the probe list, and passes it to `runFixedProbe` to also suppress claimed paths when they appear as children of an expand probe (e.g. `~/Library/Caches/Homebrew` under the `~/Library/Caches` expand). Adding a smart probe that replaces a fixed one is now a one-field annotation on the relevant fixed-probe entries instead of a remote-table sync.

Same behavior as before, fewer footguns: the claim is colocated with the probe, the set is derived from data, and `smartClaimedPaths` is gone. JSON output count matches the pre-change baseline (141 candidates).

### M5. `Category` knowledge leaks into the TUI in three places
Resolved (conservative variant). Adding a new category now requires touching three places: a new `iota` const, a new row in `scan.categoryMeta`, and a new row in `tui.categoryColors` — plus the emitter. Previously it was five (const + String case + SortOrder case + color map entry + emitter).

What changed:

- `scan/types.go` — `Category.String()` and `Category.SortOrder()` now both read from a single `categoryMeta` table holding `(name, sortOrder)` per Category. The two switch/lookup pairs that lived side-by-side after M4 are gone.
- `tui/styles.go` — `categoryColors` is now an array indexed by `scan.Category` rather than a map, mirroring the shape of `categoryMeta` on the scan side. `categoryStyle` does a bounds-checked lookup with a muted-color fallback.

What did *not* change: `Category` is still an `int` enum, not a string ID. The findings.md text proposed `type Category string` so that emitters could set `c.Category = "node_modules"` and the TUI could be the only owner of the color/order maps. I chose against it: with int enums, an emitter typo (`CatNodeModulez`) is a compile error; with string IDs (`"node_modulez"`) it's a silent miscategorisation that ships. The 5-files-vs-3-files win didn't justify trading away the compile-time safety. Re-open if a future plugin architecture (third-party emitters with their own category strings) needs it.

### M2. Fold `trash` into `cleanup`
Resolved. `internal/trash/trash.go` moved into `internal/cleanup/trash.go` as the unexported `moveToTrash` and `hardDelete` functions. The `trash.Result` struct is gone — `Execute` only ever read `.Err` from it, so the helpers now return `[]error` directly. The single dispatch site in `Execute` is correspondingly simplified. `internal/trash/` directory deleted; `internal/cleanup/`'s existing tests still pass since they exercise the `RemoveFromTrash` / `EmptyTrash` / `removeTreeCounting` path, which was untouched.

### M1. Fold `score` into `scan`
Resolved. Moved `Apply` into `internal/scan/score.go` as the unexported `applyScoring`, called as the tail of `scan.Run` right before it returns. The `internal/score/` package is gone (`score.go` + `score_test.go` moved alongside, renamed `score_test.go` to test the new in-package function). Callers (`main.go`, `internal/tui/model.go`) no longer need to remember `score.Apply(cands, cfg)` after `scan.Run` — there's no separate step to forget. `--json` output still scores correctly: 14/141 selected on a sample run with reasons populated.

### M14. `*m = *newModel(...)` mutation on rescan
Resolved. Replaced both `*m = *newModel(m.cfg, m.hardDelete)` call sites in `model.go` with `m.resetForRescan()`. The method explicitly classifies every field as either "ephemeral" (scan progress, browsing state, cleaning state, armed-toggle state, disk-usage snapshot — all zeroed/recreated) or "persists" (`cfg`, `hardDelete`, `width`, `height`, `collapsed`, `dashboardOn` — untouched). Future fields added to `model` now require a deliberate decision rather than getting silently zeroed.

Supersedes `4d5328f`'s width/height save-and-restore: those manual carry-over lines at both call sites are gone, because `resetForRescan` simply doesn't touch the fields. As a bonus, the user's collapsed-group state and dashboard toggle now survive a rescan — previously both reset to defaults each time.

### M7. Orphan config knobs
Resolved with a mixed approach: `IncludeSystemCache` wired up, `PatternsConfig.Extra.Names` deleted.

- `IncludeSystemCache` now gates the three `CatSystemCache` fixed probes (`~/Library/Caches`, `~/.cache`, `~/Library/Logs`). The gate sits inside `probeFixedPaths`'s loop: any probe whose `cat == CatSystemCache` is skipped when the knob is false. Default stays `true`, so behavior is unchanged for users who don't touch their TOML; users who *do* set `include_system_caches = false` now see what the name implies.
- `PatternsConfig` and `ExtraPatterns` are deleted. They were never read anywhere outside `Default()` round-trip, so no user could be relying on them. The `Patterns` field on `Config` goes with them. Re-add if a real ask for user-supplied classification names appears.

### M4. `Category.SortOrder()` duplicates iota declaration order
Resolved. Replaced the 13-arm switch with a single `categorySortOrder` lookup table indexed by `Category`. `SortOrder()` is now a bounds-checked array read with a 99 fallback for out-of-range values. The table is the only place display rank lives, so adding a new category is one new const + one new line in the table — same number of edits as before, but the parallel-order drift risk is gone.

### M3. Collapse parallel enum + `*Str` fields via `MarshalJSON`
Resolved. `Category`, `Safety`, and `Action` each got a `MarshalJSON` method that delegates to `String()`, so the encoder produces the same human-readable values automatically. The three `*Str` mirror fields on `Candidate` are gone, the `Category`/`Safety`/`Action` fields lose their `json:"-"` and take the public JSON tags, and `Normalize()` is gone. The Title fallback (`if Title == "" { Title = Path }`) that `Normalize` also did moves inline into `scan.addCandidate`. The two stray `c.Normalize()` calls in `score.Apply` were no-ops once mirrors disappeared and were dropped.

Field order on the marshalled struct is unchanged, so `--json` output is structurally byte-identical: same keys in the same order, same string values for every (category, safety, action) tuple. Confirmed by capturing `--json --dry-run` before and after; the only diff was timestamp drift between the two runs.

### M10. Dead-code purge
Resolved. Deleted, all confirmed unused by grep:

- `internal/scan/sizecache.go` — `resetGlobalCache` (and its accompanying `_ = config.Expand` import-keep smell). Dropped the `config` import that only existed to feed it.
- `internal/scan/fixed.go` — `disabled` field on `fixedProbe` (set nowhere) and the `if p.disabled { continue }` check.
- `internal/tui/styles.go` — `colorBg`, `itemStyle`, `pathStyle` (all defined, never referenced).
- `internal/tui/model.go` — hand-rolled `func min(a, b int) int` and `func max(a, b int) int`. Module is `go 1.25.0`, so the Go 1.21+ builtins now resolve at the call sites for free.
- `internal/scan/scan_test.go` — hand-rolled `func itoa(i int) string`. Replaced its 4 call sites with `strconv.Itoa(i)` and added the `strconv` import.

Net ~50 LOC removed; build, vet, and tests stay green.
