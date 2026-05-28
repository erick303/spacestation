package scan

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
)

// inodeKey identifies an inode uniquely across all mounted filesystems.
// Used by DirSize to skip files that hardlink to an already-counted inode,
// so a tree with hardlinks (Time Machine local snapshots, pnpm CAS-backed
// node_modules) reports its actual on-disk size instead of inflating by
// the link-count factor.
type inodeKey struct {
	dev uint64
	ino uint64
}

// DirSize computes total size of `root` using a parallel BFS with a bounded
// worker pool. Errors on individual entries (EACCES, races) are silently
// skipped so the result is best-effort. Symlinks are not followed.
//
// Hardlinks: files with Nlink > 1 are tracked by (dev, ino). The first
// occurrence is counted; later names pointing at the same inode are
// skipped. This gives "delete this whole tree and free this much" rather
// than "sum of file sizes as named" — see H3.
//
// Cancellation: walkers check ctx.Done() at the top of each per-directory
// step, so a cancel propagates within one ReadDir tick per worker (typically
// a few ms).
func DirSize(ctx context.Context, root string, workers int) int64 {
	if workers < 1 {
		workers = 4
	}
	var total int64
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	// Per-call inode-seen set, shared across walker goroutines.
	seen := make(map[inodeKey]struct{})
	var seenMu sync.Mutex

	var walk func(p string)
	walk = func(p string) {
		defer wg.Done()
		if ctx.Err() != nil {
			return
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return
		}
		for _, e := range entries {
			full := filepath.Join(p, e.Name())
			info, err := e.Info()
			if err != nil {
				continue
			}
			// Skip symlinks — both to avoid loops and so we don't double-count.
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if e.IsDir() {
				wg.Add(1)
				select {
				case sem <- struct{}{}:
					go func(path string) {
						walk(path)
						<-sem
					}(full)
				default:
					walk(full)
				}
				continue
			}
			if !info.Mode().IsRegular() {
				continue
			}
			// Only consult the inode-seen map when the file is actually
			// hardlinked (Nlink > 1). Files with a single name can't
			// duplicate, so we skip the lock entirely — keeps the common
			// case cheap.
			if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
				key := inodeKey{dev: uint64(st.Dev), ino: st.Ino}
				seenMu.Lock()
				if _, ok := seen[key]; ok {
					seenMu.Unlock()
					continue
				}
				seen[key] = struct{}{}
				seenMu.Unlock()
			}
			atomic.AddInt64(&total, info.Size())
		}
	}

	wg.Add(1)
	sem <- struct{}{}
	go func() {
		walk(root)
		<-sem
	}()
	wg.Wait()
	return atomic.LoadInt64(&total)
}
