package scan

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// DirSize computes total size of `root` using a parallel BFS with a bounded
// worker pool. Errors on individual entries (EACCES, races) are silently
// skipped so the result is best-effort. Symlinks are not followed.
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
			if info.Mode().IsRegular() {
				atomic.AddInt64(&total, info.Size())
			}
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
