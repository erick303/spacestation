//go:build darwin

package scan

import (
	"bytes"
	"encoding/binary"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// volumeInfo returns the human-facing volume name (e.g. "Macintosh HD") and the
// uppercased filesystem type (e.g. "APFS") for the filesystem mounted at path.
// Either value is "" when the lookup fails; callers must tolerate that.
func volumeInfo(path string) (name, fsType string) {
	return volumeName(path), fsTypeName(path)
}

// fsTypeName reads f_fstypename from statfs (e.g. "apfs") and uppercases it.
func fsTypeName(path string) string {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return ""
	}
	b := make([]byte, 0, len(s.Fstypename))
	for _, c := range s.Fstypename {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return strings.ToUpper(string(b))
}

// volumeName fetches the volume's display name via getattrlist(2) with
// ATTR_VOL_NAME. x/sys/unix doesn't wrap getattrlist, so we call it raw.
//
// The kernel writes a packed buffer: a u_int32 total length, then an
// attrreference_t {int32 dataoffset; uint32 length} for the name, then the
// NUL-terminated name bytes. dataoffset is relative to the attrreference's own
// address (i.e. byte 4 of the buffer).
func volumeName(path string) string {
	pathPtr, err := unix.BytePtrFromString(path)
	if err != nil {
		return ""
	}
	attr := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Volattr:     unix.ATTR_VOL_INFO | unix.ATTR_VOL_NAME,
	}
	buf := make([]byte, 4+8+256) // length + attrreference_t + name
	_, _, errno := unix.Syscall6(
		unix.SYS_GETATTRLIST,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&attr)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, // options
		0,
	)
	if errno != 0 {
		return ""
	}
	off := int32(binary.LittleEndian.Uint32(buf[4:8]))
	length := binary.LittleEndian.Uint32(buf[8:12])
	if length == 0 {
		return ""
	}
	start := 4 + int(off)
	end := start + int(length)
	if start < 4 || end > len(buf) || start >= end {
		return ""
	}
	return string(bytes.TrimRight(buf[start:end], "\x00"))
}
