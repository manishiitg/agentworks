package teamonline

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	"github.com/manishiitg/coding-agent-loop/workspace/sqlpolicy"
)

// Records are streamed through the normal bounded package/chunk transport.
// A blob is not silently converted into text, and int64 values do not pass
// through JSON floating point. Only one row is buffered at a time.
type Cell struct {
	Kind    string  `json:"kind"`
	Integer int64   `json:"integer,omitempty"`
	Real    float64 `json:"real,omitempty"`
	Text    string  `json:"text,omitempty"`
	Blob    []byte  `json:"blob,omitempty"`
}
type record struct {
	Kind    string   `json:"kind"`
	Schema  *Schema  `json:"schema,omitempty"`
	Table   string   `json:"table,omitempty"`
	Columns []string `json:"columns,omitempty"`
	Values  []Cell   `json:"values,omitempty"`
}

func cell(value any) (Cell, error) {
	switch v := value.(type) {
	case nil:
		return Cell{Kind: "null"}, nil
	case int64:
		return Cell{Kind: "integer", Integer: v}, nil
	case float64:
		return Cell{Kind: "real", Real: v}, nil
	case string:
		return Cell{Kind: "text", Text: v}, nil
	case []byte:
		return Cell{Kind: "blob", Blob: v}, nil
	default:
		return Cell{}, fmt.Errorf("unsupported dataset value")
	}
}
func (c Cell) value() (any, error) {
	switch c.Kind {
	case "null":
		return nil, nil
	case "integer":
		return c.Integer, nil
	case "real":
		return c.Real, nil
	case "text":
		return c.Text, nil
	case "blob":
		return c.Blob, nil
	default:
		return nil, fmt.Errorf("unsupported dataset cell")
	}
}

// Export writes a schema header followed by whole-table rows in one consistent
// SQLite transaction. The caller splits the stream into <=MaxFileBytes parts
// under data/ and applies the existing aggregate publication limit.
func Export(ctx context.Context, source *sql.DB, wanted Dataset, output io.Writer) (Schema, error) {
	tx, err := source.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Schema{}, err
	}
	defer tx.Rollback()
	schema, err := describe(ctx, tx, wanted)
	if err != nil {
		return Schema{}, err
	}
	var total int64
	write := func(row record) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if int64(len(data)) >= publication.MaxFileBytes {
			return fmt.Errorf("one dataset row exceeds the ordinary file limit")
		}
		total += int64(len(data) + 1)
		if total > publication.MaxBytes {
			return fmt.Errorf("dataset exceeds the publication limit")
		}
		data = append(data, '\n')
		_, err = output.Write(data)
		return err
	}
	if err = write(record{Kind: "schema", Schema: &schema}); err != nil {
		return schema, err
	}
	for _, table := range schema.Tables {
		fields, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_xinfo(?) WHERE hidden=0 ORDER BY cid`, table.Name)
		if err != nil {
			return schema, err
		}
		var columns, quoted []string
		for fields.Next() {
			var name string
			if err = fields.Scan(&name); err != nil {
				fields.Close()
				return schema, err
			}
			columns = append(columns, name)
			quoted = append(quoted, quote(name))
		}
		err = fields.Err()
		fields.Close()
		if err != nil {
			return schema, err
		}
		// Preserve accessible implicit rowids too: views/triggers may use them,
		// even when the authored schema has no INTEGER PRIMARY KEY column.
		var withoutRowID int
		if err = tx.QueryRowContext(ctx, "SELECT wr FROM pragma_table_list WHERE schema='main' AND name=?", table.Name).Scan(&withoutRowID); err != nil {
			return schema, err
		}
		if withoutRowID == 0 {
			used := map[string]bool{}
			for _, name := range columns {
				used[strings.ToLower(name)] = true
			}
			for _, alias := range []string{"rowid", "_rowid_", "oid"} {
				if !used[alias] {
					columns = append([]string{alias}, columns...)
					quoted = append([]string{quote(alias)}, quoted...)
					break
				}
			}
		}

		rows, err := tx.QueryContext(ctx, "SELECT "+strings.Join(quoted, ",")+" FROM "+quote(table.Name))
		if err != nil {
			return schema, err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			refs := make([]any, len(values))
			for i := range values {
				refs[i] = &values[i]
			}
			if err = rows.Scan(refs...); err != nil {
				rows.Close()
				return schema, err
			}
			cells := make([]Cell, len(values))
			for i, value := range values {
				cells[i], err = cell(value)
				if err != nil {
					rows.Close()
					return schema, err
				}
			}
			if err = write(record{Kind: "row", Table: table.Name, Columns: columns, Values: cells}); err != nil {
				rows.Close()
				return schema, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return schema, err
		}
	}
	return schema, write(record{Kind: "end"})
}

// Import is additive and transactional. On every code-only update it returns
// before reading even a byte of the data package. Seed is a server-only value
// derived by the receive-online mapping, never a request flag.
func Import(ctx context.Context, destination *sql.DB, input io.Reader, seed bool) error {
	if !seed {
		return nil
	}
	scanner := bufio.NewScanner(io.LimitReader(input, publication.MaxBytes+1))
	scanner.Buffer(make([]byte, 64<<10), int(publication.MaxFileBytes))
	var decoded int64
	read := func(row *record) error {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			return io.EOF
		}
		decoded += int64(len(scanner.Bytes()) + 1)
		if decoded > publication.MaxBytes {
			return fmt.Errorf("dataset exceeds publication limit")
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(row); err != nil {
			return err
		}
		if decoder.Decode(new(any)) != io.EOF {
			return fmt.Errorf("dataset record has trailing contents")
		}
		return nil
	}
	var header record
	if err := read(&header); err != nil {
		return err
	}
	if header.Kind != "schema" || header.Schema == nil {
		return fmt.Errorf("dataset schema is missing")
	}
	schema := header.Schema
	if len(schema.Objects) > publication.MaxFiles {
		return fmt.Errorf("too many dataset schema objects")
	}
	conn, err := destination.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Only this private connection's import transaction disables FK execution;
	// all copied rows are checked before commit, including cyclic parent sets.
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys=ON")
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tables := map[string]bool{}
	for _, object := range schema.Objects {
		if err := sqlpolicy.ValidateDatasetSchema(object.SQL, object.Kind, object.Name); err != nil {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE lower(name)=lower(?)", object.Name).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("Team dataset name %q already exists; nothing was replaced", object.Name)
		}
		if object.Kind == "table" {
			if _, err = tx.ExecContext(ctx, object.SQL); err != nil {
				return err
			}
			tables[object.Name] = true
		}
	}
	for {
		var row record
		if err = read(&row); err != nil {
			return fmt.Errorf("dataset is incomplete: %w", err)
		}
		if row.Kind == "end" {
			break
		}
		if row.Kind != "row" || !tables[row.Table] || len(row.Columns) == 0 || len(row.Columns) != len(row.Values) {
			return fmt.Errorf("invalid dataset row")
		}
		names, parameters := make([]string, len(row.Columns)), make([]string, len(row.Columns))
		values := make([]any, len(row.Values))
		for i, name := range row.Columns {
			names[i] = quote(name)
			parameters[i] = "?"
			values[i], err = row.Values[i].value()
			if err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO "+quote(row.Table)+" ("+strings.Join(names, ",")+") VALUES ("+strings.Join(parameters, ",")+")", values...); err != nil {
			return err
		}
	}
	if err := read(new(record)); err != io.EOF {
		return fmt.Errorf("dataset has unexpected trailing contents")
	}
	// Install indexes/views/triggers only after the rows, so importing existing
	// data does not execute its triggers. Normal writes execute them afterwards.
	for _, object := range schema.Objects {
		if (object.Kind == "index" || object.Kind == "trigger") && !tables[object.Table] {
			return fmt.Errorf("dataset object points outside the copied tables")
		}
		if object.Kind != "table" {
			if _, err = tx.ExecContext(ctx, object.SQL); err != nil {
				return err
			}
		}
		var kind, table string
		if err = tx.QueryRowContext(ctx, "SELECT type,tbl_name FROM sqlite_schema WHERE name=?", object.Name).Scan(&kind, &table); err != nil {
			return err
		}
		if kind != object.Kind || table != object.Table {
			return fmt.Errorf("dataset object does not match its declared table")
		}
	}
	wanted := Dataset{}
	for name := range tables {
		wanted.Tables = append(wanted.Tables, name)
	}
	for _, object := range schema.Objects {
		if object.Kind == "view" {
			wanted.Views = append(wanted.Views, object.Name)
		}
		if object.Kind == "trigger" {
			wanted.Triggers = append(wanted.Triggers, object.Name)
		}
	}
	if _, err = describe(ctx, tx, wanted); err != nil {
		return err
	}
	for name, value := range schema.Sequences {
		if !tables[name] || value < 0 {
			return fmt.Errorf("invalid dataset sequence")
		}
		if _, err = tx.ExecContext(ctx, "UPDATE sqlite_sequence SET seq=? WHERE name=?", value, name); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO sqlite_sequence(name,seq) SELECT ?,? WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name=?)", name, value, name); err != nil {
			return err
		}
	}
	for name := range tables {
		violations, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check("+quote(name)+")")
		if err != nil {
			return err
		}
		bad := violations.Next()
		readErr := violations.Err()
		violations.Close()
		if readErr != nil {
			return readErr
		}
		if bad {
			return fmt.Errorf("dataset has missing foreign-key rows")
		}
	}

	return tx.Commit()
}
