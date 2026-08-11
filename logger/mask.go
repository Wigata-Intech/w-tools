package logger

import "strings"

// maskString applies m to s, operating on runes so multi-byte characters
// mask cleanly. When s has no middle to hide (len <= ShowFirst+ShowLast),
// the whole value is masked.
func maskString(s string, m Mask) string {
	c := m.MaskChar
	if c == 0 {
		c = '*'
	}
	first, last := m.ShowFirst, m.ShowLast
	if first < 0 {
		first = 0
	}
	if last < 0 {
		last = 0
	}
	r := []rune(s)
	n := len(r)
	// Overflow-safe form of n <= first+last: huge bounds must mask fully,
	// never wrap negative and slip past the guard.
	if first >= n || last >= n-first {
		return strings.Repeat(string(c), n)
	}
	var b strings.Builder
	b.WriteString(string(r[:first]))
	b.WriteString(strings.Repeat(string(c), n-first-last))
	b.WriteString(string(r[n-last:]))
	return b.String()
}
