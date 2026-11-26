package sqlpolicy

import (
	"fmt"
	"strings"
)

// ValidateDatasetSchema admits one named source schema object for item-only
// dataset transfer. It is not a general migration permission. In particular,
// CTAS, attached/temp schemas and SQL after a trigger body are not admitted.
func ValidateDatasetSchema(input, kind, name string) error {
	if len(input) > maximumMutationSQL {
		return fmt.Errorf("dataset schema is too large")
	}
	tokens, err := tokenizeSQLForPolicy(input)
	if err != nil {
		return err
	}
	plain := func(i int, word string) bool { return i < len(tokens) && !tokens[i].quoted && tokens[i].word == word }
	if !plain(0, "CREATE") {
		return fmt.Errorf("dataset objects must be CREATE statements")
	}
	i := 1
	if kind == "index" && plain(i, "UNIQUE") {
		i++
	}
	if !plain(i, strings.ToUpper(kind)) {
		return fmt.Errorf("dataset schema kind does not match its object")
	}
	i++
	switch kind {
	case "table", "index", "view", "trigger":
	default:
		return fmt.Errorf("unsupported dataset object")
	}
	if plain(i, "IF") {
		if !plain(i+1, "NOT") || !plain(i+2, "EXISTS") {
			return fmt.Errorf("invalid dataset schema")
		}
		i += 3
	}
	if i >= len(tokens) || !tokens[i].identifier || !strings.EqualFold(tokens[i].word, name) {
		return fmt.Errorf("dataset schema name does not match its object")
	}
	i++
	if i < len(tokens) && tokens[i].symbol == '.' {
		return fmt.Errorf("dataset schema must stay in its own database")
	}
	if kind == "table" && (i >= len(tokens) || tokens[i].symbol != '(') {
		return fmt.Errorf("dataset tables must declare columns, not select data")
	}
	if kind != "trigger" {
		if hasAdditionalSQLStatement(input) {
			return fmt.Errorf("dataset schema must contain one statement")
		}
		return nil
	}
	// A trigger has semicolons inside BEGIN/END; CASE/END is an expression, not
	// the end of its body. Quoted text/comments were removed by the same lexer
	// used for ordinary managed mutations; SQLite validates the remaining grammar.
	begun, cases, ended := false, 0, false
	for ; i < len(tokens); i++ {
		token := tokens[i]
		if ended {
			if token.symbol != ';' || i != len(tokens)-1 {
				return fmt.Errorf("SQL after a dataset trigger is not allowed")
			}
			continue
		}
		if token.quoted {
			continue
		}
		switch token.word {
		case "BEGIN":
			if begun {
				return fmt.Errorf("nested dataset trigger body is not allowed")
			}
			begun = true
		case "CASE":
			if begun {
				cases++
			}
		case "END":
			if begun {
				if cases > 0 {
					cases--
				} else {
					ended = true
				}
			}
		}
	}
	if !begun || !ended {
		return fmt.Errorf("dataset trigger body is incomplete")
	}
	return nil
}
