package migrationx

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var (
	errUnknownAnnotation = errors.New("unknown migrationx annotation")
	errNestedRegion      = errors.New("statement begin inside an open statement region")
	errUnclosedRegion    = errors.New("statement region never closed")
	errStrayEnd          = errors.New("statement end without a begin")
	errUnterminated      = errors.New("unterminated quote or comment at end of file")
)

// script is one migration file parsed into executable statements.
type script struct {
	statements []string
	noTx       bool
}

// scanState tracks the SQL lexical context across lines: quotes and
// block comments legally span statements and lines.
type scanState int

const (
	scanNormal scanState = iota
	scanSingleQuote
	scanDoubleQuote
	scanBacktick
	scanBlockComment
)

// parseScript splits a migration file into statements on top-level
// semicolons, aware of quotes, comments, and the migrationx annotations:
//
//	-- migrationx:no-transaction
//	-- migrationx:statement begin
//	-- migrationx:statement end
//
// Between begin and end nothing splits — trigger and procedure bodies
// keep their semicolons. backslashEscapes matches the dialect: mysql
// escapes quotes with backslash, sqlite treats backslash literally.
// Unknown migrationx: annotations, unterminated quotes, and unclosed
// regions are errors: this parser feeds Exec, so ambiguity aborts.
func parseScript(src string, backslashEscapes bool) (script, error) {
	var s script
	var buf strings.Builder
	state := scanNormal
	inRegion := false

	flush := func() {
		if stmt := strings.TrimSpace(buf.String()); isExecutable(stmt, backslashEscapes) {
			s.statements = append(s.statements, stmt)
		}
		buf.Reset()
	}

	for n, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if state == scanNormal && strings.Contains(trimmed, "migrationx:") &&
			!strings.HasPrefix(trimmed, "-- migrationx:") &&
			(strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "#")) {
			return script{}, fmt.Errorf("line %d: %w: %q", n+1, errUnknownAnnotation, trimmed)
		}
		if state == scanNormal && strings.HasPrefix(trimmed, "-- migrationx:") {
			switch strings.TrimPrefix(trimmed, "-- migrationx:") {
			case "no-transaction":
				s.noTx = true
			case "statement begin":
				if inRegion {
					return script{}, fmt.Errorf("line %d: %w", n+1, errNestedRegion)
				}
				flush()
				inRegion = true
			case "statement end":
				if !inRegion {
					return script{}, fmt.Errorf("line %d: %w", n+1, errStrayEnd)
				}
				inRegion = false
				flush()
			default:
				return script{}, fmt.Errorf("line %d: %w: %q", n+1, errUnknownAnnotation, trimmed)
			}
			continue
		}

		state = scanLine(&buf, line, state, inRegion, backslashEscapes, flush)
	}

	if inRegion {
		return script{}, errUnclosedRegion
	}
	if state != scanNormal {
		return script{}, errUnterminated
	}
	flush()
	return s, nil
}

// scanLine feeds one line through the lexical state machine, splitting
// on top-level semicolons unless the scanner is inside a region.
func scanLine(buf *strings.Builder, line string, state scanState, inRegion, backslashEscapes bool, flush func()) scanState {
	i := 0
	for i < len(line) {
		c := line[i]
		switch state {
		case scanSingleQuote, scanDoubleQuote, scanBacktick:
			buf.WriteByte(c)
			if backslashEscapes && c == '\\' && state != scanBacktick && i+1 < len(line) {
				buf.WriteByte(line[i+1])
				i += 2
				continue
			}
			if c == quoteChar(state) {
				state = scanNormal
			}
			i++
		case scanBlockComment:
			buf.WriteByte(c)
			if c == '*' && i+1 < len(line) && line[i+1] == '/' {
				buf.WriteByte('/')
				state = scanNormal
				i++
			}
			i++
		default:
			switch {
			case c == '-' && i+1 < len(line) && line[i+1] == '-',
				c == '#' && backslashEscapes:
				buf.WriteString(line[i:])
				i = len(line)
			case c == '/' && i+1 < len(line) && line[i+1] == '*':
				buf.WriteString("/*")
				state = scanBlockComment
				i += 2
			case c == '\'':
				buf.WriteByte(c)
				state = scanSingleQuote
				i++
			case c == '"':
				buf.WriteByte(c)
				state = scanDoubleQuote
				i++
			case c == '`':
				buf.WriteByte(c)
				state = scanBacktick
				i++
			case c == ';' && !inRegion:
				flush()
				i++
			default:
				buf.WriteByte(c)
				i++
			}
		}
	}
	buf.WriteByte('\n')
	return state
}

// quoteChar returns the closing byte for a quote state.
func quoteChar(state scanState) byte {
	switch state {
	case scanSingleQuote:
		return '\''
	case scanDoubleQuote:
		return '"'
	default:
		return '`'
	}
}

// isExecutable reports whether a statement contains anything beyond
// comments and whitespace — databases reject comment-only queries, so
// the scanner never emits one. Rune-wise, like strings.TrimSpace, so the
// decision is identical when a statement is re-scanned.
func isExecutable(stmt string, hashComments bool) bool {
	rs := []rune(stmt)
	inBlock := false
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if inBlock {
			if r == '*' && i+1 < len(rs) && rs[i+1] == '/' {
				inBlock = false
				i++
			}
			continue
		}
		switch {
		case r == '-' && i+1 < len(rs) && rs[i+1] == '-',
			r == '#' && hashComments:
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
		case r == '/' && i+1 < len(rs) && rs[i+1] == '*':
			inBlock = true
			i++
		case unicode.IsSpace(r):
		default:
			return true
		}
	}
	return false
}
