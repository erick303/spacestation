package scan

import "testing"

func TestParseDockerSize(t *testing.T) {
	// Fractional cases: bind the float through a variable so the conversion
	// is at runtime (matching the float64→int64 truncation parseDockerSize
	// does after ParseFloat), not a compile-time constant that Go refuses
	// to fold to int64.
	n12 := 1.2
	oneTwoGB := int64(n12 * 1024 * 1024 * 1024)
	n395 := 3.95
	threeNineFiveGB := int64(n395 * 1024 * 1024 * 1024)
	cases := []struct {
		in   string
		want int64
	}{
		{"0B", 0},
		{"1B", 1},
		{"512B", 512},
		{"1KB", 1024},
		{"1.5KB", 1024 + 512},
		{"1MB", 1024 * 1024},
		{"42MB", 42 * 1024 * 1024},
		{"1GB", 1024 * 1024 * 1024},
		{"1.2GB", oneTwoGB},
		{"1TB", 1024 * 1024 * 1024 * 1024},

		// Case-insensitive units (some CLIs emit lowercase).
		{"1gb", 1024 * 1024 * 1024},

		// Whitespace between number and unit, as brew --dry-run emits.
		{"3.95 GB", threeNineFiveGB},
		{"  100 MB", 100 * 1024 * 1024}, // anchored regex still needs leading digit; this case actually returns 0
	}
	for _, tc := range cases {
		got := parseDockerSize(tc.in)
		// The leading-whitespace case is the only one that diverges from
		// "the obvious answer" because the regex is anchored with ^[0-9.].
		// Pin it explicitly so the assertion below doesn't pretend it parses.
		if tc.in == "  100 MB" {
			if got != 0 {
				t.Errorf("parseDockerSize(%q) = %d, want 0 (anchored regex rejects leading whitespace)", tc.in, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("parseDockerSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseDockerSizeRejectsGarbage(t *testing.T) {
	rejects := []string{
		"",
		"NaN",
		"foo",
		"GB",          // unit without number
		"1XB",         // unknown unit
		"-5MB",        // anchored regex rejects leading minus
		"1.2.3GB",     // ParseFloat would reject "1.2.3"
	}
	for _, s := range rejects {
		if got := parseDockerSize(s); got != 0 {
			t.Errorf("parseDockerSize(%q) = %d, want 0", s, got)
		}
	}
}

func TestHumanBytesShort(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1024 B"}, // 1 KB is below the MB threshold; intentional — we only branch at MB and GB
		{1024 * 1024, "1 MB"},
		{int64(1.5 * 1024 * 1024), "2 MB"},          // MB uses precision 0 → rounds
		{1024 * 1024 * 1024, "1.0 GB"},
		{int64(2.5 * 1024 * 1024 * 1024), "2.5 GB"}, // GB uses precision 1
	}
	for _, tc := range cases {
		if got := humanBytesShort(tc.in); got != tc.want {
			t.Errorf("humanBytesShort(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
