package teamonline

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	"github.com/manishiitg/coding-agent-loop/workspace/sqlpolicy"
)

type Object struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Table    string `json:"table"`
	SQL      string `json:"sql"`
	RootPage int64  `json:"-"`
}
type Schema struct {
	Sequences map[string]int64 `json:"sequences,omitempty"`
	Objects   []Object         `json:"objects"`
	Tables    []Table          `json:"tables"`
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// Describe uses one SQLite read transaction, not filesystem copies of a live
// DB/WAL. It closes foreign-key dependencies, and asks SQLite's own compiled
// query plans about view/trigger table access rather than guessing SQL names.
func Describe(ctx context.Context, db *sql.DB, wanted Dataset) (Schema, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Schema{}, err
	}
	defer tx.Rollback()
	return describe(ctx, tx, wanted)
}

func describe(ctx context.Context, tx *sql.Tx, wanted Dataset) (Schema, error) {
	out := Schema{}
	objects := map[string]Object{}
	roots := map[int64]string{}
	var schemaBytes int64
	rows, err := tx.QueryContext(ctx, `SELECT type,name,tbl_name,coalesce(sql,''),rootpage FROM sqlite_schema WHERE type IN ('table','view','index','trigger')`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var object Object
		if err = rows.Scan(&object.Kind, &object.Name, &object.Table, &object.SQL, &object.RootPage); err != nil {
			rows.Close()
			return out, err
		}
		schemaBytes += int64(len(object.SQL) + len(object.Name) + len(object.Table))
		if len(objects) >= publication.MaxFiles || schemaBytes > publication.MaxFileBytes {
			rows.Close()
			return out, fmt.Errorf("database schema exceeds the normal item publication limits")
		}
		objects[strings.ToLower(object.Name)] = object
		if object.RootPage > 0 {
			roots[object.RootPage] = strings.ToLower(object.Table)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	tables := map[string]bool{}
	selected := map[string]bool{}
	var addTable func(string) error
	addTable = func(name string) error {
		key := strings.ToLower(name)
		if tables[key] {
			return nil
		}
		object, ok := objects[key]
		if !ok || object.Kind != "table" || strings.HasPrefix(key, "sqlite_") {
			return fmt.Errorf("dataset needs table %q", name)
		}
		if err := sqlpolicy.ValidateDatasetSchema(object.SQL, "table", object.Name); err != nil {
			return fmt.Errorf("table %q: %w", name, err)
		}
		if len(selected) >= publication.MaxFiles {
			return fmt.Errorf("too many dataset dependencies")
		}
		tables[key] = true
		selected[key] = true
		fk, err := tx.QueryContext(ctx, `SELECT "table" FROM pragma_foreign_key_list(?)`, object.Name)
		if err != nil {
			return err
		}
		var parents []string
		for fk.Next() {
			var parent string
			if err = fk.Scan(&parent); err != nil {
				fk.Close()
				return err
			}
			parents = append(parents, parent)
		}
		err = fk.Err()
		fk.Close()
		if err != nil {
			return err
		}
		for _, parent := range parents {
			if err := addTable(parent); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range wanted.Tables {
		if err := addTable(name); err != nil {
			return out, err
		}
	}
	for kind, names := range map[string][]string{"view": wanted.Views, "trigger": wanted.Triggers} {
		for _, name := range names {
			key := strings.ToLower(name)
			object, ok := objects[key]
			if !ok || object.Kind != kind {
				return out, fmt.Errorf("dataset needs %s %q", kind, name)
			}
			if err := sqlpolicy.ValidateDatasetSchema(object.SQL, kind, object.Name); err != nil {
				return out, err
			}
			if kind == "trigger" && !tables[strings.ToLower(object.Table)] {
				return out, fmt.Errorf("declare trigger table %q in this item's dataset", object.Table)
			}
			selected[key] = true
		}
	}
	for key, object := range objects {
		if object.Kind == "index" && object.SQL != "" && tables[strings.ToLower(object.Table)] {
			if err := sqlpolicy.ValidateDatasetSchema(object.SQL, "index", object.Name); err != nil {
				return out, err
			}
			selected[key] = true
		}
		if object.Kind == "trigger" && tables[strings.ToLower(object.Table)] && !selected[key] {
			return out, fmt.Errorf("declare trigger %q in this item's dependencies before putting it online", object.Name)
		}
	}
	// OpenRead/OpenWrite identify actual B-trees, including subqueries and
	// trigger programs. Ephemeral cursors have different opcodes and are ignored.
	check := func(statement string) error {
		plan, err := tx.QueryContext(ctx, "EXPLAIN "+statement)
		if err != nil {
			return err
		}
		defer plan.Close()
		for plan.Next() {
			var address, p1, p2, p3, p5 int64
			var opcode string
			var p4, comment any
			if err := plan.Scan(&address, &opcode, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
				return err
			}
			if (opcode == "OpenRead" || opcode == "OpenWrite" || opcode == "ReopenIdx") && p3 == 0 {
				if dependency := roots[p2]; dependency != "" && !strings.HasPrefix(dependency, "sqlite_") && !tables[dependency] {
					return fmt.Errorf("declare table %q: this item's view or trigger uses it", objects[dependency].Name)
				}
			}
		}
		return plan.Err()
	}
	for key := range selected {
		object := objects[key]
		if object.Kind == "view" {
			if err := check("SELECT * FROM " + quote(object.Name)); err != nil {
				return out, err
			}
		}
	}
	for key := range tables {
		object := objects[key]
		columns, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_xinfo(?) WHERE hidden=0 ORDER BY cid`, object.Name)
		if err != nil {
			return out, err
		}
		var assignments, lengths []string
		for columns.Next() {
			var name string
			if err = columns.Scan(&name); err != nil {
				columns.Close()
				return out, err
			}
			col := quote(name)
			assignments = append(assignments, col+"="+col)
			lengths = append(lengths, "coalesce(length(cast("+col+" as blob)),0)")
		}
		err = columns.Err()
		columns.Close()
		if err != nil {
			return out, err
		}
		for _, statement := range []string{"INSERT INTO " + quote(object.Name) + " DEFAULT VALUES", "UPDATE " + quote(object.Name) + " SET " + strings.Join(assignments, ","), "DELETE FROM " + quote(object.Name)} {
			if err := check(statement); err != nil {
				return out, fmt.Errorf("table %q dependency preflight: %w", object.Name, err)
			}
		}
		table := Table{Name: object.Name}
		if err := tx.QueryRowContext(ctx, "SELECT count(*),coalesce(sum("+strings.Join(lengths, "+")+"),0) FROM "+quote(object.Name)).Scan(&table.Rows, &table.Bytes); err != nil {
			return out, err
		}
		out.Tables = append(out.Tables, table)
	}
	if _, ok := objects["sqlite_sequence"]; ok {
		for key := range tables {
			var value int64
			err := tx.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name=?", objects[key].Name).Scan(&value)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return out, err
			}
			if out.Sequences == nil {
				out.Sequences = map[string]int64{}
			}
			out.Sequences[objects[key].Name] = value
		}
	}

	for key := range selected {
		out.Objects = append(out.Objects, objects[key])
	}
	order := map[string]int{"table": 0, "index": 1, "view": 2, "trigger": 3}
	sort.Slice(out.Objects, func(i, j int) bool {
		a, b := out.Objects[i], out.Objects[j]
		if order[a.Kind] != order[b.Kind] {
			return order[a.Kind] < order[b.Kind]
		}
		return a.Name < b.Name
	})
	sort.Slice(out.Tables, func(i, j int) bool { return out.Tables[i].Name < out.Tables[j].Name })
	return out, nil
}
