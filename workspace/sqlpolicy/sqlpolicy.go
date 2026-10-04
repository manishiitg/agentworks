package sqlpolicy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const maximumMutationSQL = 100_000
const maximumPragmaErrors = 1_000

var (
	readPragmaPattern      = regexp.MustCompile(`(?is)^pragma\s+(?:main\.)?([a-z_]+)\s*(?:\(([^;]*)\))?\s*;?$`)
	positiveIntegerPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	sqlIdentifierPattern   = regexp.MustCompile(`(?s)^(?:[A-Za-z_][A-Za-z0-9_]*|"(?:[^"]|"")+"|'(?:[^']|'')+'|\[[^\]]+\]|` + "`(?:[^`]|``)+`" + `)$`)
)

// isSafeReadPragma deliberately recognizes PRAGMA names and their argument
// shapes instead of growing one permissive regular expression. Every entry in
// this allowlist is observational; assignments and unknown pragmas are denied.
func isSafeReadPragma(input string) bool {
	matches := readPragmaPattern.FindStringSubmatch(strings.TrimSpace(input))
	if matches == nil {
		return false
	}
	name := strings.ToLower(matches[1])
	argument := strings.TrimSpace(matches[2])
	hasArgument := strings.Contains(matches[0], "(")

	switch name {
	case "table_info", "table_xinfo", "index_list", "index_info", "index_xinfo", "foreign_key_list":
		return hasArgument && argument != "" && sqlIdentifierPattern.MatchString(argument)
	case "database_list", "journal_mode", "user_version", "schema_version":
		return !hasArgument
	case "integrity_check", "quick_check":
		if !hasArgument {
			return true
		}
		if !positiveIntegerPattern.MatchString(argument) {
			return false
		}
		limit, err := strconv.Atoi(argument)
		return err == nil && limit <= maximumPragmaErrors
	case "foreign_key_check":
		return !hasArgument || sqlIdentifierPattern.MatchString(argument)
	default:
		return false
	}
}

// stripSQLCommentsAndSpace removes leading whitespace and comments so policy is
// applied to the actual first token rather than a model-controlled prefix.
func stripSQLCommentsAndSpace(input string) string {
	remaining := input
	for {
		remaining = strings.TrimSpace(remaining)
		switch {
		case strings.HasPrefix(remaining, "--"):
			if newline := strings.IndexByte(remaining, '\n'); newline >= 0 {
				remaining = remaining[newline+1:]
				continue
			}
			return ""
		case strings.HasPrefix(remaining, "/*"):
			if end := strings.Index(remaining[2:], "*/"); end >= 0 {
				remaining = remaining[end+4:]
				continue
			}
			return remaining
		default:
			return remaining
		}
	}
}

// hasAdditionalSQLStatement rejects stacked SQL while allowing one optional
// trailing semicolon. It understands SQLite strings, identifiers and comments.
func hasAdditionalSQLStatement(input string) bool {
	const (
		plain = iota
		singleQuote
		doubleQuote
		backtickQuote
		bracketQuote
		lineComment
		blockComment
	)
	state := plain
	for i := 0; i < len(input); i++ {
		ch := input[i]
		next := byte(0)
		if i+1 < len(input) {
			next = input[i+1]
		}
		switch state {
		case plain:
			switch {
			case ch == '\'':
				state = singleQuote
			case ch == '"':
				state = doubleQuote
			case ch == '`':
				state = backtickQuote
			case ch == '[':
				state = bracketQuote
			case ch == '-' && next == '-':
				state = lineComment
				i++
			case ch == '/' && next == '*':
				state = blockComment
				i++
			case ch == ';':
				return stripSQLCommentsAndSpace(input[i+1:]) != ""
			}
		case singleQuote:
			if ch == '\'' {
				if next == '\'' {
					i++
				} else {
					state = plain
				}
			}
		case doubleQuote:
			if ch == '"' {
				if next == '"' {
					i++
				} else {
					state = plain
				}
			}
		case backtickQuote:
			if ch == '`' {
				state = plain
			}
		case bracketQuote:
			if ch == ']' {
				state = plain
			}
		case lineComment:
			if ch == '\n' {
				state = plain
			}
		case blockComment:
			if ch == '*' && next == '/' {
				state = plain
				i++
			}
		}
	}
	return false
}

func firstSQLKeyword(input string) string {
	input = stripSQLCommentsAndSpace(input)
	end := 0
	for end < len(input) {
		ch := input[end]
		if ch < 'A' || ch > 'Z' && ch < 'a' || ch > 'z' {
			break
		}
		end++
	}
	return strings.ToUpper(input[:end])
}

type sqlPolicyToken struct {
	word       string
	symbol     byte
	identifier bool
	quoted     bool
}

// tokenizeSQLForPolicy is a deliberately small lexer for the mutation policy.
// It does not try to validate SQLite syntax; SQLite still does that. Its job is
// only to distinguish CTE structure from quoted text/comments so the policy can
// identify the top-level statement that follows a WITH clause without guessing
// from a substring.
func tokenizeSQLForPolicy(input string) ([]sqlPolicyToken, error) {
	var tokens []sqlPolicyToken
	for i := 0; i < len(input); {
		ch := input[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '\f':
			i++
		case ch == '-' && i+1 < len(input) && input[i+1] == '-':
			i += 2
			for i < len(input) && input[i] != '\n' {
				i++
			}
		case ch == '/' && i+1 < len(input) && input[i+1] == '*':
			end := strings.Index(input[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated block comment")
			}
			i += end + 4
		case ch == '\'' || ch == '"' || ch == '`':
			quote := ch
			quotedIdentifier := quote != '\''
			i++
			quotedStart := i
			closed := false
			for i < len(input) {
				if input[i] != quote {
					i++
					continue
				}
				if i+1 < len(input) && input[i+1] == quote {
					i += 2
					continue
				}
				i++
				closed = true
				break
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted value")
			}
			word := ""
			if quotedIdentifier {
				word = strings.ToUpper(strings.ReplaceAll(input[quotedStart:i-1], string(quote)+string(quote), string(quote)))
			}
			tokens = append(tokens, sqlPolicyToken{word: word, identifier: quotedIdentifier, quoted: quotedIdentifier})
		case ch == '[':
			i++
			bracketStart := i
			closed := false
			for i < len(input) {
				if input[i] == ']' {
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted identifier")
			}
			tokens = append(tokens, sqlPolicyToken{word: strings.ToUpper(input[bracketStart : i-1]), identifier: true, quoted: true})
		case ch == '(' || ch == ')' || ch == ',' || ch == ';':
			tokens = append(tokens, sqlPolicyToken{symbol: ch})
			i++
		case ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z':
			start := i
			i++
			for i < len(input) {
				current := input[i]
				if current == '_' || current == '$' || current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' || current >= '0' && current <= '9' {
					i++
					continue
				}
				break
			}
			tokens = append(tokens, sqlPolicyToken{word: strings.ToUpper(input[start:i]), identifier: true})
		default:
			tokens = append(tokens, sqlPolicyToken{symbol: ch})
			i++
		}
	}
	return tokens, nil
}

func skipSQLPolicyParentheses(tokens []sqlPolicyToken, start int) (int, error) {
	if start >= len(tokens) || tokens[start].symbol != '(' {
		return start, fmt.Errorf("expected parenthesized CTE query")
	}
	depth := 0
	for i := start; i < len(tokens); i++ {
		switch tokens[i].symbol {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1, nil
			}
			if depth < 0 {
				return i, fmt.Errorf("unbalanced parentheses")
			}
		}
	}
	return len(tokens), fmt.Errorf("unterminated parenthesized CTE query")
}

// statementKeywordAfterWith parses only the SQLite WITH-clause envelope and
// returns the top-level statement keyword after its CTE definitions. This keeps
// the mutation endpoint fail-closed: WITH ... SELECT/CREATE/PRAGMA are not
// authorized merely because a nested or quoted INSERT token exists.
func statementKeywordAfterWith(input string) (string, error) {
	tokens, err := tokenizeSQLForPolicy(input)
	if err != nil {
		return "", err
	}
	if len(tokens) == 0 || tokens[0].word != "WITH" {
		return "", fmt.Errorf("expected WITH")
	}
	index := 1
	if index < len(tokens) && tokens[index].word == "RECURSIVE" {
		index++
	}
	for {
		if index >= len(tokens) || !tokens[index].identifier {
			return "", fmt.Errorf("expected CTE name")
		}
		index++
		if index < len(tokens) && tokens[index].symbol == '(' {
			index, err = skipSQLPolicyParentheses(tokens, index)
			if err != nil {
				return "", err
			}
		}
		if index >= len(tokens) || tokens[index].word != "AS" {
			return "", fmt.Errorf("expected AS after CTE name")
		}
		index++
		if index < len(tokens) && tokens[index].word == "NOT" {
			index++
			if index >= len(tokens) || tokens[index].word != "MATERIALIZED" {
				return "", fmt.Errorf("expected MATERIALIZED after NOT")
			}
			index++
		} else if index < len(tokens) && tokens[index].word == "MATERIALIZED" {
			index++
		}
		index, err = skipSQLPolicyParentheses(tokens, index)
		if err != nil {
			return "", err
		}
		if index < len(tokens) && tokens[index].symbol == ',' {
			index++
			continue
		}
		if index >= len(tokens) || tokens[index].word == "" {
			return "", fmt.Errorf("expected statement after WITH clause")
		}
		return tokens[index].word, nil
	}
}

func ValidateRead(input string) error {
	trimmed := stripSQLCommentsAndSpace(input)
	if trimmed == "" {
		return fmt.Errorf("sql cannot be empty")
	}
	if len(trimmed) > maximumMutationSQL {
		return fmt.Errorf("sql exceeds %d bytes", maximumMutationSQL)
	}
	if hasAdditionalSQLStatement(trimmed) {
		return fmt.Errorf("exactly one SQL statement is allowed")
	}
	if strings.Contains(strings.ToLower(trimmed), "load_extension") {
		return fmt.Errorf("extension loading is not allowed")
	}
	switch firstSQLKeyword(trimmed) {
	case "SELECT", "WITH", "EXPLAIN":
		return nil
	case "PRAGMA":
		if isSafeReadPragma(trimmed) {
			return nil
		}
		return fmt.Errorf("pragma is not in the read-only allowlist")
	default:
		return fmt.Errorf("only SELECT, read-only WITH/EXPLAIN, and safe schema PRAGMA statements are allowed")
	}
}

func ValidateMutation(input string) error {
	trimmed := stripSQLCommentsAndSpace(input)
	if trimmed == "" {
		return fmt.Errorf("sql cannot be empty")
	}
	if len(trimmed) > maximumMutationSQL {
		return fmt.Errorf("sql exceeds %d bytes", maximumMutationSQL)
	}
	if hasAdditionalSQLStatement(trimmed) {
		return fmt.Errorf("exactly one SQL statement is allowed")
	}
	keyword := firstSQLKeyword(trimmed)
	if keyword == "WITH" {
		var err error
		keyword, err = statementKeywordAfterWith(trimmed)
		if err != nil {
			return fmt.Errorf("invalid WITH mutation: %w", err)
		}
	}
	switch keyword {
	case "INSERT", "UPDATE", "DELETE":
		return nil
	default:
		return fmt.Errorf("only INSERT, UPDATE, and DELETE, optionally prefixed by WITH, are allowed; schema changes use workflow migrations")
	}
}

// MutationTarget identifies the top-level DML target after validation. It
// supports CTEs and quoted identifiers without interpreting SQL values.
func MutationTarget(input string) (string, error) {
	if err := ValidateMutation(input); err != nil {
		return "", err
	}
	tokens, err := tokenizeSQLForPolicy(input)
	if err != nil {
		return "", err
	}
	depth := 0
	for i, t := range tokens {
		if t.symbol == '(' {
			depth++
			continue
		}
		if t.symbol == ')' {
			depth--
			continue
		}
		if depth != 0 || t.quoted || (t.word != "INSERT" && t.word != "UPDATE" && t.word != "DELETE") {
			continue
		}
		j := i + 1
		if j < len(tokens) && tokens[j].word == "OR" {
			j += 2
		}
		if j < len(tokens) && (tokens[j].word == "INTO" || tokens[j].word == "FROM") {
			j++
		}
		if j >= len(tokens) || tokens[j].word == "" {
			return "", fmt.Errorf("missing mutation table")
		}
		name := tokens[j].word
		if j+1 < len(tokens) && tokens[j+1].symbol == '.' {
			if name != "MAIN" || j+2 >= len(tokens) {
				return "", fmt.Errorf("unsupported database namespace")
			}
			name = tokens[j+2].word
		}
		return strings.ToLower(name), nil
	}
	return "", fmt.Errorf("missing mutation target")
}
