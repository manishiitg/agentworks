package sqlpolicy

import "testing"

func TestMutationTarget(t *testing.T) {
	for _, sql := range []string{`CREATE UNIQUE INDEX IF NOT EXISTS groups_unique ON groups(id)`, `CREATE TABLE IF NOT EXISTS groups AS SELECT 1 AS id`, `CREATE TABLE IF NOT EXISTS main."groups" (id TEXT)`, `CREATE INDEX IF NOT EXISTS main."groups_idx" ON "groups"(id)`, `/* setup */ CREATE TABLE IF NOT EXISTS [groups] (id TEXT DEFAULT ';');`, `UPDATE groups SET name=?`, `INSERT INTO "groups"(id) VALUES(?)`, `DELETE FROM main.[groups] WHERE id=?`, `WITH "UPDATE" AS (SELECT 1) UPDATE groups SET name=?`, `WITH x(a) AS (SELECT ')') INSERT INTO groups(id) SELECT a FROM x`, `UPDATE /*comment*/ OR IGNORE groups SET name=?`} {
		target, err := MutationTarget(sql)
		if err != nil || target != "groups" {
			t.Fatalf("%s: %s %v", sql, target, err)
		}
	}
	for _, sql := range []string{`UPDATE groups SET name='x';DELETE FROM groups`, `WITH x AS (SELECT 1) SELECT * FROM groups`, `CREATE TABLE groups(id TEXT)`, `ALTER TABLE groups ADD COLUMN note TEXT`, `DROP TABLE IF EXISTS groups`, `CREATE TABLE IF NOT EXISTS other.groups(id TEXT)`, `CREATE TABLE IF NOT EXISTS groups(id TEXT); DROP TABLE groups`, `CREATE INDEX IF NOT EXISTS idx ON other.groups(id)`, `ATTACH DATABASE 'other' AS other`, `UPDATE other.groups SET name='x'`} {
		if _, err := MutationTarget(sql); err == nil {
			t.Fatal(sql)
		}
	}
}

// PLAT-746-1023: a source schema record is not permission to run arbitrary
// migrations in the Team's database. Trigger body semicolons remain valid.
func TestDatasetSchemaCannotAttachOrStackStatements(t *testing.T) {
	for _, tc := range []struct {
		kind, name, sql string
		allowed         bool
	}{
		{"table", "orders", `CREATE TABLE orders(id INTEGER PRIMARY KEY, value TEXT)`, true},
		{"table", "orders", `CREATE TABLE orders AS SELECT * FROM other`, false},
		{"view", "summary", `CREATE VIEW summary AS SELECT 'x; DROP TABLE orders' AS value`, true},
		{"view", "summary", `CREATE VIEW summary AS SELECT 1; ATTACH 'other.sqlite' AS other`, false},
		{"table", "orders", `CREATE TABLE other.orders(id INTEGER)`, false},
		{"trigger", "orders_update", `CREATE TRIGGER orders_update AFTER UPDATE ON orders BEGIN UPDATE orders SET value=CASE WHEN NEW.value IS NULL THEN 'END; DROP' ELSE NEW.value END WHERE id=NEW.id; END;`, true},
		{"trigger", "orders_update", `CREATE TRIGGER orders_update AFTER UPDATE ON orders BEGIN SELECT 1; END; DROP TABLE orders;`, false},
	} {
		if err := ValidateDatasetSchema(tc.sql, tc.kind, tc.name); (err == nil) != tc.allowed {
			t.Errorf("dataset %s allowed=%t: %v", tc.kind, tc.allowed, err)
		}
	}
}
