package sqliteopen

import (
	"database/sql/driver"
	"fmt"
	"regexp"

	"modernc.org/sqlite"
)

// The same managed SQLite functions must be present in the workspace API and
// the item dataset importer. Keep the existing implementation here rather than
// making valid source views fail only when copied to the server process.
// SQLite's REGEXP operator ("X REGEXP Y") has no built-in implementation --
// it is a syntax hook that requires an application-registered regexp(Y, X)
// scalar function (SQLite docs: "X REGEXP Y is equivalent to regexp(Y,X)").
// Without this, any schema-declared query using REGEXP fails uniformly with
// "no such function: REGEXP", even though the SQL itself is otherwise valid
// (PLAT-238). modernc.org/sqlite supports registering one; mattn/go-sqlite3
// would need the same treatment if this driver ever changes. Registration is
// process-global and applies to every connection opened after it runs, so
// this init() covers both openQueryOnlyDB and openMutationDB.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("regexp", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil || args[1] == nil {
			// NULL/non-text operands: SQLite's own comparison operators return
			// NULL rather than erroring when an operand is NULL.
			return nil, nil
		}
		pattern, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("regexp: pattern argument must be text, got %T", args[0])
		}
		value, ok := args[1].(string)
		if !ok {
			return nil, fmt.Errorf("regexp: value argument must be text, got %T", args[1])
		}
		matched, err := regexp.MatchString(pattern, value)
		if err != nil {
			return nil, fmt.Errorf("regexp: invalid pattern %q: %w", pattern, err)
		}
		return matched, nil
	})
}
