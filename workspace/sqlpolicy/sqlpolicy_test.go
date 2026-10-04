package sqlpolicy

import "testing"

func TestMutationTarget(t *testing.T) {
	for _, sql := range []string{`UPDATE groups SET name=?`, `INSERT INTO "groups"(id) VALUES(?)`, `DELETE FROM main.[groups] WHERE id=?`, `WITH "UPDATE" AS (SELECT 1) UPDATE groups SET name=?`, `WITH x(a) AS (SELECT ')') INSERT INTO groups(id) SELECT a FROM x`, `UPDATE /*comment*/ OR IGNORE groups SET name=?`} {
		target, err := MutationTarget(sql)
		if err != nil || target != "groups" {
			t.Fatalf("%s: %s %v", sql, target, err)
		}
	}
	for _, sql := range []string{`UPDATE groups SET name='x';DELETE FROM groups`, `WITH x AS (SELECT 1) SELECT * FROM groups`, `ATTACH DATABASE 'other' AS other`, `UPDATE other.groups SET name='x'`} {
		if _, err := MutationTarget(sql); err == nil {
			t.Fatal(sql)
		}
	}
}
