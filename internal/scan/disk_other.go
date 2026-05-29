//go:build !darwin

package scan

// volumeInfo has no portable implementation off macOS; the dashboard falls back
// to a generic title when both values are empty.
func volumeInfo(_ string) (name, fsType string) { return "", "" }
