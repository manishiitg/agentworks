//go:build !linux && !darwin

package crewroot

func checkMigrationSpace(docs, state string, entries []privateMove) error { return nil }
