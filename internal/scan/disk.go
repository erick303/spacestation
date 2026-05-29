package scan

import "syscall"

// DiskUsage describes a single filesystem's capacity at a point in time.
type DiskUsage struct {
	Total int64 // bytes
	Free  int64 // bytes available to a non-superuser
	Used  int64 // Total - Free

	// VolumeName is the human-facing volume name (e.g. "Macintosh HD"), and
	// FSType the uppercased filesystem type (e.g. "APFS"). Either may be empty
	// when the platform can't supply it — callers must tolerate that.
	VolumeName string
	FSType     string
}

// GetDiskUsage returns capacity info for the filesystem containing `path`.
// Returns a zeroed DiskUsage on any error so callers don't have to branch.
func GetDiskUsage(path string) DiskUsage {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return DiskUsage{}
	}
	total := int64(s.Bsize) * int64(s.Blocks)
	free := min(int64(s.Bsize)*int64(s.Bavail), total)
	du := DiskUsage{Total: total, Free: free, Used: total - free}
	du.VolumeName, du.FSType = volumeInfo(path)
	return du
}
