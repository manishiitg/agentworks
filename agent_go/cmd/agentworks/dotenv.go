package main

import (
	"fmt"
	"strings"
)

// dotenvFile is a parsed .env file. Invalid lists lines that were skipped, by number and reason only: a reason never
// contains any part of a value, so it is safe to print.
type dotenvFile struct {
	Values  map[string]string
	Order   []string
	Invalid []string
}

// parseDotEnv reads KEY=VALUE lines: blank lines and # comments are skipped, an `export ` prefix is allowed, a value may
// be unquoted (an inline " #" comment is cut), single or backtick quoted (literal) or double quoted (\n \r \t \" \\ are
// unescaped). Quoted values may span lines. A name that is not letters, digits and underscores (not starting with a
// digit) is skipped and reported. The last of duplicate names wins.
func parseDotEnv(text string) dotenvFile {
	out := dotenvFile{Values: map[string]string{}}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	skip := func(n int, reason string) { out.Invalid = append(out.Invalid, fmt.Sprintf("line %d: %s", n, reason)) }
	for i := 0; i < len(lines); i++ {
		n := i + 1
		line := strings.TrimSpace(lines[i])
		if i == 0 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "export"); ok && (strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")) {
			line = strings.TrimSpace(rest)
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			skip(n, "no '=' found")
			continue
		}
		name := strings.TrimSpace(line[:eq])
		valid := secretNameForCLI(name)
		rest := strings.TrimLeft(line[eq+1:], " \t")
		var value string
		switch {
		case strings.HasPrefix(rest, `"`):
			var closed bool
			value, closed, i = readQuoted(lines, i, rest[1:], '"')
			if !closed {
				skip(n, "unterminated double quote")
				continue
			}
			value = unescapeDoubleQuoted(value)
		case strings.HasPrefix(rest, "'"), strings.HasPrefix(rest, "`"):
			var closed bool
			value, closed, i = readQuoted(lines, i, rest[1:], rest[0])
			if !closed {
				skip(n, "unterminated quote")
				continue
			}
		default:
			value = rest
			if cut := strings.Index(value, " #"); cut >= 0 {
				value = value[:cut]
			}
			if cut := strings.Index(value, "\t#"); cut >= 0 {
				value = value[:cut]
			}
			value = strings.TrimSpace(value)
		}
		if !valid {
			skip(n, "invalid name (letters, digits and underscores, not starting with a digit)")
			continue
		}
		if value == "" {
			skip(n, "empty value")
			continue
		}
		if _, seen := out.Values[name]; !seen {
			out.Order = append(out.Order, name)
		}
		out.Values[name] = value
	}
	return out
}

// readQuoted reads from the text after an opening quote up to the closing quote, continuing on following lines. It
// returns the raw inner text, whether it closed, and the index of the last line consumed.
func readQuoted(lines []string, at int, first string, quote byte) (string, bool, int) {
	var b strings.Builder
	current := first
	for {
		for j := 0; j < len(current); j++ {
			if current[j] == '\\' && quote == '"' && j+1 < len(current) {
				b.WriteByte(current[j])
				b.WriteByte(current[j+1])
				j++
				continue
			}
			if current[j] == quote {
				return b.String(), true, at
			}
			b.WriteByte(current[j])
		}
		if at+1 >= len(lines) {
			return "", false, at
		}
		at++
		b.WriteByte('\n')
		current = lines[at]
	}
}

func unescapeDoubleQuoted(raw string) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) {
			switch raw[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '"', '\\':
				b.WriteByte(raw[i+1])
			default:
				b.WriteByte(raw[i])
				b.WriteByte(raw[i+1])
			}
			i++
			continue
		}
		b.WriteByte(raw[i])
	}
	return b.String()
}

// secretNameForCLI is the server's secret-name rule: letters, digits and underscores, not starting with a digit.
func secretNameForCLI(name string) bool {
	return brainSecretNamePattern.MatchString(name)
}
