package tmuxparse

import "strings"

// DecodeEscapedValue decodes tmux %output and %extended-output payloads where
// non-printable bytes and backslash are escaped as \ooo octal sequences.
func DecodeEscapedValue(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}

		if i+1 < len(s) && s[i+1] == '\\' {
			b.WriteByte('\\')
			i++
			continue
		}

		if i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			v := (s[i+1]-'0')*64 + (s[i+2]-'0')*8 + (s[i+3] - '0')
			b.WriteByte(v)
			i += 3
			continue
		}

		// Keep unknown escape forms as-is.
		b.WriteByte('\\')
	}

	return b.String()
}

func isOctal(ch byte) bool {
	return ch >= '0' && ch <= '7'
}
