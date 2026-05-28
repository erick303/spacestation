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

### H1. Rescan leaks the previous scan's goroutine
**File:** `internal/tui/model.go:237, 299` (rescan); `internal/tui/model.go:132-143` (`startScan`)
**Verification:** confirmed. `startScan` runs `scan.Run` synchronously inside a `tea.Cmd` closure with `defer cancel()` — meaning the context is cancelled *only* when scan.Run returns naturally. On `r` rescan, `*m = *newModel(...)` replaces the model and re-issues `Init()`, but the previous `scan.Run` keeps walking the filesystem to completion, still holding workers and FDs. Every rescan during a scan compounds this.

**Fix:** Store the `cancel` func on the model. On rescan / quit, call cancel before reassigning. Thread `ctx` into `DirSize`/`CachedDirSize` so the walkers actually observe the cancel (today only `walkProjects` checks `ctx.Done()`, and only at the top of each `walk` call — `DirSize` doesn't take a ctx at all).

### H2. `q` during scanning doesn't cancel the scan goroutine
**File:** `internal/tui/model.go:208-209, 132-143`
**Verification:** confirmed, same root cause as H1. `stageScanning` handles `q`/`ctrl+c` by returning `tea.Quit`, which tears down the program — but the scan goroutine launched by the tea.Cmd is detached and runs to completion. On a large cold scan you can't actually quit.

**Fix:** same as H1 — hoist the cancel onto the model and call it before `tea.Quit`.

### H3. Hardlink double-counting in `DirSize`
**File:** `internal/scan/size.go:28-53`
**Verification:** confirmed. `info.Size()` is reported per name, not per inode. Hardlink-heavy trees (Time Machine local snapshots, pnpm CAS store, some `.cargo` caches) inflate by the link-count factor. For a CAS-style store this can be 2–10×.

**Fix:** maintain `map[uint64]struct{}` of seen `(dev, ino)` inside the walk, gated by a small mutex (or per-walk so no sharing needed). Skip files whose inode was already counted.

### H4. Pad/truncate use `len()` (bytes), not display width
**Files:**
- `internal/tui/format.go:73-78` (`truncatePath`)
- `internal/tui/model.go:817-828` (`padRight`, `padLeft`)
- `internal/tui/dashboard.go:170, 182-208` (`sepW` hardcoded to 3 to dodge this)

**Verification:** confirmed. `truncatePath` slices on byte indices and will cut mid-rune on any path with non-ASCII (CJK, emoji, accented chars). `padRight`/`padLeft` count bytes for column width, so a path with one multi-byte char produces a column off by N bytes worth of padding. Compounding factor: `renderItemRow` styles strings with lipgloss (ANSI escapes) and then calls `padRight` (`model.go:704`) — `len()` over an ANSI-styled string includes the escape bytes, breaking alignment further. The dashboard's `sepW = 3` is the smoking gun: someone hand-tuned around `len()` already.

**Fix:** use `lipgloss.Width()` for padding decisions and `runewidth` (already transitively pulled in) or `[]rune` slicing for truncation. Or extract a single `displayPad(s, n int)` helper and route everything through it.

### H5. Size-cache key ignores inode/device
**File:** `internal/scan/sizecache.go:104-107`
**Verification:** confirmed. Cache key is the path string only. Renaming a directory or remounting a different volume at the same path will hit-with-wrong-data if mtimes happen to match (cross-volume mtime equality is unlikely but not impossible; rename within a volume preserves mtime exactly).

**Fix:** include `(dev, ino)` from `syscall.Stat_t` in `sizeCacheEntry` and verify on hit. Cost: one extra `os.Stat` field per check, near-free.

### H6. `score.Apply` mis-handles zero/future mtime
**File:** `internal/score/score.go:21-22, 35-38`
**Verification:** confirmed. `LastTouched` returns `time.Time{}` on stat failure (`recency.go:14`). `age := now.Sub(zeroTime)` is ~734,000 days; `age > minAge` is true; if `Safety == SafetyRegenerable`, the entry is **auto-selected** with the reason `"Stale 734159d, regenerable"`. That's a directory we couldn't stat being silently armed for deletion. Future-mtime (clock skew, restored backup) is the cosmetic mirror — negative age renders as `(-5d)` and never selects, less serious.

**Fix:** `if c.LastTouched.IsZero() { c.Reason = "unknown age — not auto-selecting"; continue }` at the top of the loop. Optionally clamp future ages to 0.

### H7. `stageCleaning` swallows `ctrl+c`
**File:** `internal/tui/model.go:228-229`
**Verification:** confirmed. The whole switch in `stageCleaning` is `return m, nil` — no key handled. On a multi-minute `docker system prune` or `pip cache purge` the user is stuck. The mockup at `docs/tui-mockup.html:162` advertised "esc cancel after current step"; not delivered.

**Fix:** at minimum, handle `ctrl+c` → `tea.Quit`. Better, thread a `context.Context` into `cleanup.Execute` and cancel on `esc` between command-action items.

### H8. `tea.WithMouseCellMotion()` enabled with zero mouse handlers
**File:** `internal/tui/model.go:23`
**Verification:** confirmed. Enabling mouse motion breaks native text-selection / copy-paste in most terminal emulators — users have to hold Option (iTerm) or Shift to copy text. There are zero `tea.MouseMsg` cases anywhere in Update.

**Fix:** remove `tea.WithMouseCellMotion()`. Add it back when an actual mouse interaction is implemented.

---

## MEDIUM — design / coherence

### M1. Fold `score` into `scan`
**Files:** `internal/score/score.go` (47 LOC), called from `internal/tui/model.go:138` and `main.go:71`.
**Verification:** confirmed. Single consumer pattern (twice), no interface, no tests. The package adds an import edge and a "remember to call score.Apply after scan.Run" footgun. Recommend `scan.RunWithDefaults(ctx, opts) []Candidate` or moving `Apply` to `scan/score.go` and folding into the tail of `scan.Run`.

### M2. Fold `trash` into `cleanup`
**Files:** `internal/trash/trash.go` (90 LOC), called from `internal/cleanup/cleanup.go:62, 64, 75`.
**Verification:** confirmed. One caller, no abstraction, no tests. The split adds a package boundary without an interface or alternate implementation. Recommend unexported `moveToTrash` / `hardDelete` helpers in `cleanup`.

### M3. `Candidate` has parallel enum + `*Str` fields
**File:** `internal/scan/types.go:122-156`
**Verification:** confirmed. `Category/CategoryStr`, `Safety/SafetyStr`, `Action/ActionStr` exist purely so JSON output gets human-readable strings. `Normalize()` materializes them. Forgetting to call it leaves the JSON fields empty — there are exactly two call sites (`scan.go:58` via `addCandidate`, `score.go:45`); any future code path that constructs a Candidate must remember.

**Fix:** implement `MarshalJSON` on `Category`/`Safety`/`Action` (or `encoding.TextMarshaler`) and delete the `*Str` fields and `Normalize()`.

### M4. `Category.SortOrder()` duplicates iota declaration order
**File:** `internal/scan/types.go:55-84` vs `:7-21`
**Verification:** confirmed. The switch encodes ~the same order as the iota constants except GoCache and Xcode are swapped (iota: GoCache=5, Xcode=6; sort: Xcode=5, GoCache=6) and Docker and Homebrew similarly. Two parallel orderings will drift the next time someone adds a category. Use a `[...]int{}` lookup table indexed by `int(c)`, or — if no order swaps are needed — reorder the iota constants and use `int(c)` directly.

### M5. `Category` knowledge leaks into the TUI in three places
**Files:** `internal/scan/types.go:55-84` (`SortOrder`), `internal/tui/styles.go:63-83` (`categoryColors`), `internal/scan/classify.go` + `fixed.go` + `smart.go` (emitters).
**Verification:** confirmed. Adding a category requires touching: types.go (const), `String()`, `SortOrder()`, styles.go (color map), plus the emitter. Five files.

**Fix:** make `Category` a string ID (`type Category string` with const values like `"node_modules"`). Centralize order/color in TUI keyed by ID. Or, at minimum, move `SortOrder` to the TUI — it's a presentation concern.

### M6. `smartClaimedPaths()` exists only to dodge dedupe
**File:** `internal/scan/fixed.go:81, 105-133`
**Verification:** confirmed. The dedupe pass in `scan.Run:115-143` already keys by path and prefers `ActionCommand`. So `smartClaimedPaths` is a pre-filter that has to be hand-kept in sync with every smart probe. If you add a smart probe and forget to update `smartClaimedPaths`, you get a duplicate that dedupe silently cleans up later — but the redundant `CachedDirSize` work already ran.

**Fix:** drop `smartClaimedPaths` and trust the dedupe pass. Or, conversely, drop the dedupe and structure probes as a single list of `{path, sizer, optionalSmartCmd}`.

### M7. Orphan config knobs
**File:** `internal/config/config.go:24, 41-43`
**Verification:** confirmed by grep — neither `IncludeSystemCache` nor `PatternsConfig.Extra.Names` is read anywhere outside `Default()` / round-trip. Both are dead.

**Fix:** delete them, or wire them up:
- `IncludeSystemCache` should gate the `~/Library/Caches` / `~/.cache` fixed probes in `fixed.go:58-61`.
- `Extra.Names` should be merged into `classifyDir` (probably with a fixed category like `CatOther` and a generic detail string).

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

### M10. Dead code (delete-only)
**Verification:** all confirmed by grep.
- `internal/scan/sizecache.go:131-137` — `resetGlobalCache` is unused; the `_ = config.Expand` line is a smell that exists only to keep the unused-after-deletion `config` import alive. Delete both, drop the import.
- `internal/scan/fixed.go:19, 85-87` — `disabled` field on `fixedProbe` is never set. Delete field and the check.
- `internal/tui/styles.go:15` — `colorBg` is unused.
- `internal/tui/styles.go:35` — `itemStyle` is unused.
- `internal/tui/styles.go:46` — `pathStyle` is unused.
- `internal/tui/model.go:829-840` — hand-rolled `min`/`max`. Module is `go 1.25.0` (`go.mod:3`); use Go 1.21+ builtins.
- `internal/scan/scan_test.go:171-190` — hand-rolled `itoa`. Replace with `strconv.Itoa(i)`.

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

### M14. `*m = *newModel(...)` mutation on rescan
**File:** `internal/tui/model.go:237, 299`
**Verification:** confirmed. Works because Update has a pointer receiver, but: (a) every new field added to `model` either has to zero-value sensibly on rescan or its meaning silently changes; (b) interacts badly with H1 (the previous scan's goroutine still references `m.progressCh` via the old closure).

**Fix:** `func (m *model) reset()` that resets only the fields that should reset, leaving things like `cfg`, `hardDelete`, `width`, `height`, `collapsed` (debatable) alone.

### M15. Initial walk goroutine is unnecessary
**Files:** `internal/scan/scan.go:225-230`, `internal/scan/size.go:57-62`
**Verification:** confirmed. Both spawn a goroutine that the caller immediately waits for via `wg.Wait()`. Just call `walk(root)` directly on the caller's goroutine (still need `wg.Add(1)` since `walk` defers `wg.Done()`).

---

## MEDIUM — defensive / latent

### M16. `SaveSizeCache` marshals the map without holding the lock
**File:** `internal/scan/sizecache.go:68-90`
**Verification:** confirmed but currently safe. `c.mu.RLock(); entries := c.entries; c.mu.RUnlock()` — `entries` is a map reference, not a copy. `json.Marshal` iterates it without the lock. Current call sites (`tui/model.go:140, 505`, `main.go:72`) all run after the scan completes, so no concurrent writer exists. Latent: any future concurrent caller will panic on concurrent map iter+write.

**Fix:** either hold `RLock` across the marshal, or copy the map under the lock.

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

### Hy3. No `--version` flag / build-info embedding
**File:** `main.go:17-29`
**Verification:** confirmed by inspection. For a tool installed via `go install`, users can't report which version they're running.
**Fix:** add `--version` that prints `runtime/debug.ReadBuildInfo()` VCS revision. Zero-config, no ldflags needed under Go 1.18+.

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
