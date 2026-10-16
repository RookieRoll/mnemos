// Package contextfmt provides transient formatting helpers shared by context consumers.
package contextfmt

// Estimate returns the existing byte-based token estimate, rounded up. It is
// not a tokenizer guarantee; callers must include framing and annotations.
func Estimate(text string) int {
	return (len(text) + 3) / 4
}

// Choose returns the first complete representation that fits after prefix.
// Empty representations are never selected.
func Choose(prefix string, budget int, forms ...string) string {
	for _, form := range forms {
		if form != "" && Estimate(prefix+form) <= budget {
			return form
		}
	}
	return ""
}
