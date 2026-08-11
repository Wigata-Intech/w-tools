package logger

// MaskString re-exports maskString for the blackbox fuzz tests — the
// documented exception to the blackbox rule: the masking invariants deserve
// direct fuzzing, and going through the full handler would slow the fuzzer
// without adding coverage.
func MaskString(s string, m Mask) string {
	return maskString(s, m)
}
