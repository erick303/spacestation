package scan

import "syscall"

// DiskUsage describes a single filesystem's capacity at a point in time.
type DiskUsage struct {
	Total int64 // bytes
	Free  int64 // bytes available to a non-superuser
	Used  int64 // Total - Free
}

// GetDiskUsage returns capacity info for the filesystem containing `path`.
// Returns a zeroed DiskUsage on any error so callers don't have to branch.
func GetDiskUsage(path string) DiskUsage {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return DiskUsage{}
	}
	total := int64(s.Bsize) * int64(s.Blocks)
	free := int64(s.Bsize) * int64(s.Bavail)
	if free > total {
		free = total
	}
	return DiskUsage{Total: total, Free: free, Used: total - free}
}
