package scan

import "testing"

func TestIsScreenshotName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// macOS defaults
		{"Screenshot 2026-01-12 at 09.43.30.png", true},
		{"Screenshot 2026-01-12 at 09.43.30 (2).png", true}, // duplicate suffix
		{"Screenshot 2026-01-12 at 09.43.30.jpg", true},     // alternate format
		{"Screenshot 2026-01-12 at 09.43.30.HEIC", true},    // case-insensitive ext
		// Not screenshots
		{"screenshot-lowercase.png", false}, // wrong prefix case
		{"My Screenshot.png", false},        // prefix not at start
		{"Screenshot 2026.txt", false},      // non-image extension
		{"Screen Shot 2020-01-01.png", false}, // legacy two-word name (intentionally skipped)
		{"vacation.png", false},
		{"Screenshot", false}, // prefix only, no extension
	}
	for _, tc := range cases {
		if got := isScreenshotName(tc.name); got != tc.want {
			t.Errorf("isScreenshotName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
