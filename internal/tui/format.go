package tui

import (
	"fmt"
	"time"
)

func humanBytes(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	f := float64(n)
	switch {
	case n >= tb:
		return fmt.Sprintf("%.2f TB", f/tb)
	case n >= gb:
		return fmt.Sprintf("%.2f GB", f/gb)
	case n >= mb:
		return fmt.Sprintf("%.1f MB", f/mb)
	case n >= kb:
		return fmt.Sprintf("%.0f KB", f/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func humanAge(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	days := int(d / (24 * time.Hour))
	switch {
	case days <= 0:
		hours := int(d / time.Hour)
		if hours <= 0 {
			return "just now"
		}
		return fmt.Sprintf("%dh ago", hours)
	case days < 30:
		return fmt.Sprintf("%dd ago", days)
	case days < 365:
		return fmt.Sprintf("%dmo ago", days/30)
	default:
		return fmt.Sprintf("%dy ago", days/365)
	}
}

func truncatePath(p string, max int) string {
	if len(p) <= max {
		return p
	}
	return "…" + p[len(p)-max+1:]
}
