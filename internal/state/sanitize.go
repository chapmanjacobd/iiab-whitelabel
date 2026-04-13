package state

import "strings"

// SanitizeSubdomain converts a demo name to a valid subdomain string.
// It lowercases the input, keeps only alphanumeric characters and hyphens,
// trims leading/trailing hyphens, and returns "demo" if the result is empty.
func SanitizeSubdomain(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, s)
	s = strings.Trim(s, "-")
	if s == "" {
		s = "demo"
	}
	return s
}
