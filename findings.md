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

## MEDIUM — design / coherence

### M6. `smartClaimedPaths()` exists only to dodge dedupe
**File:** `internal/scan/fixed.go:81, 105-133`
**Verification:** confirmed. The dedupe pass in `scan.Run:115-143` already keys by path and prefers `ActionCommand`. So `smartClaimedPaths` is a pre-filter that has to be hand-kept in sync with every smart probe. If you add a smart probe and forget to update `smartClaimedPaths`, you get a duplicate that dedupe silently cleans up later — but the redundant `CachedDirSize` work already ran.

**Fix:** drop `smartClaimedPaths` and trust the dedupe pass. Or, conversely, drop the dedupe and structure probes as a single list of `{path, sizer, optionalSmartCmd}`.

### M8. TUI knows literal `"hard"` string
**File:** `internal/tui/model.go:493, 762, 796`
**Verification:** confirmed. The mode resolution happens three times via string compare. `cleanup.Mode` is already a typed int.

**Fix:** resolve once in `main.go` and pass `cleanup.Mode` into `tui.Run`.

### M9. Mockup-vs-delivered: cleaning UI is one spinner
**Files:** `internal/tui/model.go:778-783` (delivered); `docs/tui-mockup.html:146-163` (promised).
**Verification:** confirmed. Mockup showed per-step progress bar, item counter, live command output, batch checkmarks. Delivered: a single spinner line "please wait — Finder is moving files to Trash." Combined with H7 (no cancel), a 5-minute Docker prune looks like the tool froze.

**Fix:** non-trivial — needs `cleanup.Execute` to stream results via a channel and the TUI to subscribe. Worth the effort if cleanups will routinely take minutes.

---

## MEDIUM — code quality / dead code

### M11. `probeBrewSmart` reuses `parseDockerSize` by concatenating regex groups
**File:** `internal/scan/smart.go:135-142` vs `:66-90`
**Verification:** confirmed. Brew's regex splits `(num)(unit)`; the code then concatenates `m[1]+m[2]` and feeds back to `parseDockerSize` which immediately re-splits with the same pattern. Plus `sizeRe` (line 136) is recompiled inside the loop body each call.

**Fix:** extract `humanSize(num float64, unit string) int64`; promote `sizeRe` to a package-level var; rename `parseDockerSize` to `parseHumanSize` or have it take pre-split arguments.

### M12. Unbounded fan-out under expand-probes
**File:** `internal/scan/fixed.go:166-192`
**Verification:** confirmed. For `~/Library/Caches` with N children (often 100–300), this spawns N goroutines, each calling `CachedDirSize(..., workers)` which itself uses up to `workers` goroutines. Worst case: ~N × workers = thousands of concurrent FDs. `os.ReadDir` errors are then swallowed (`size.go:25, scan.go:177`) producing 0-size candidates that get filtered out — silent data loss.

**Fix:** bounded child semaphore (`make(chan struct{}, workers)`).

### M13. `recency.go` does an extra `Lstat` per entry
**File:** `internal/scan/recency.go:23`
**Verification:** confirmed. `DirEntry.Info()` returns the already-populated stat info (Lstat semantics on Unix-like) with no extra syscall on most platforms.

**Fix:** replace `os.Lstat(filepath.Join(root, e.Name()))` with `e.Info()`.

### M15. Initial walk goroutine is unnecessary
**Files:** `internal/scan/scan.go:225-230`, `internal/scan/size.go:57-62`
**Verification:** confirmed. Both spawn a goroutine that the caller immediately waits for via `wg.Wait()`. Just call `walk(root)` directly on the caller's goroutine (still need `wg.Add(1)` since `walk` defers `wg.Done()`).

---

## MEDIUM — defensive / latent

### M17. AppleScript path escaping uses `%q`, not AppleScript escaping
**File:** `internal/trash/trash.go:33`
**Verification:** partially confirmed; severity downgraded. Go's `%q` and AppleScript string syntax happen to agree on the common escapes (`\"`, `\\`, `\n`, `\r`, `\t`) and on printable Unicode. Where they differ: Go's `%q` emits `\xNN` for non-printable bytes; AppleScript doesn't support `\x`. macOS filenames can't contain NULL but can contain other control chars (rare but possible). The realistic failure mode is the batched osascript returning an error → previously this triggered the silent Hard-delete fallback (see C1).

**Fix:** sanitize explicitly (replace `\` then `"`), or invoke osascript with the paths as arguments via `osascript -e 'on run argv …' arg1 arg2 …`. Lower priority than C1; once C1 is fixed this becomes a "weird filename fails to trash and surfaces an error" situation, which is fine.

---

## LOW — UX & polish

### L1. `c` "clear-all" is one key from `ctrl+c`
**File:** `internal/tui/model.go:287`
**Verification:** confirmed. Reflexively hitting `c` to "cancel" silently nukes the entire selection with no undo. The flash hint that fires on enter-with-zero-selected (`model.go:294-295`) is the only safety net.

**Fix:** remap to `X` or `shift+C`, or require a two-press confirmation analogous to the group-toggle arming at `model.go:316-323`. At minimum, set a flash like `"Cleared %d selections (press u/A to undo)"`.

### L2. No `?` help / no `/` filter / no `o` open-in-Finder
**File:** keybindings in `internal/tui/model.go:245-305`
**Verification:** confirmed. Two-line help at the bottom covers the basics; for a 500+ row list with 14 bindings the conventions are a `?` modal, `/` filter, and `o` reveal. The list also has no scroll-position indicator.

**Fix:** at minimum a `?` overlay. Filter and reveal are larger items but would change the product feel.

### L3. README key table missing several bindings
**File:** `README.md:84-93`
**Verification:** confirmed. Code has `[`, `]`, `{`, `}`, `g`, `G`, `v`, `pgup`, `pgdown`. README lists none of those.

**Fix:** extend the table to cover the implemented bindings.

### L4. README references missing demo assets
**File:** `README.md:9, 11`
**Verification:** confirmed. `docs/hero.png` and `docs/demo.gif` don't exist (only `docs/demo.tape` and `docs/tui-mockup.html` do). Renders as broken-image links on GitHub.

**Fix:** generate via `vhs docs/demo.tape` and commit, or remove the references until they're regenerated.

### L5. README says "Requires Go 1.22+" but go.mod requires 1.25
**File:** `README.md:63` vs `go.mod:3`
**Verification:** confirmed contradiction. `go 1.25.0` is a minimum (since Go 1.21), so users on 1.22 will fail to build. CI is also pinned to 1.25 (`.github/workflows/ci.yml:18`). README is the outlier.

**Fix:** either lower go.mod's directive (no language features above 1.22 are used in the source, on inspection) and align CI — best for portability — or update the README to say "Requires Go 1.25+".

---

## HYGIENE — remaining gaps

Most hygiene items the original review flagged are already in place (LICENSE, .gitignore, CI workflow, docs/demo.tape).

### Hy1. No git remote configured and no tags
**Verification:** `git remote -v` returns empty; `git tag` returns empty.
**Impact:** README's `git clone https://github.com/erick303/spacestation` won't resolve until the repo is pushed; `go install github.com/erick303/spacestation@latest` won't work without at least one tag.
**Fix:** push to `github.com/erick303/spacestation`, tag `v0.1.0`, update README install snippet to lead with `go install ...@latest`.

### Hy2. No `//go:build darwin` constraints; macOS-only code compiles on Linux
**Files:** `internal/trash/trash.go` (osascript), `internal/scan/fixed.go` (`~/Library/...`), parts of `internal/scan/smart.go` (`xcrun simctl`), `internal/scan/disk.go` (`syscall.Statfs_t` — actually portable, but Statfs semantics differ).
**Verification:** confirmed by `grep -r "//go:build" .` returning nothing.
**Impact:** the tool builds cleanly on Linux and explodes at runtime when `osascript` isn't on PATH.
**Fix:** add `//go:build darwin` to `trash.go` and to the macOS-specific bits, or a `runtime.GOOS != "darwin"` check at `main.go` startup that prints a clear error.

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
