package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// The Crew database is user data, unlike the platform's run/audit databases.
// SQLite reads committed WAL rows into a standalone image while the capture
// lock is held. No live database/WAL file enters the binary transfer.
func captureCrewMoveDatabase(ctx context.Context, roots crewroot.Roots, snapshot *crewsync.Snapshot) error {
	source, err := crewMoveRecordPath(roots, "db/db.sqlite")
	if err != nil {
		return err
	}
	if _, err = os.Stat(source); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	destination := filepath.Join(snapshot.StagingDir, "database.sqlite")
	if err = snapshotCrewMoveDatabase(ctx, source, destination); err != nil {
		return err
	}
	snapshot.DatabasePath = destination
	return nil
}

func snapshotCrewMoveDatabase(ctx context.Context, source, destination string) error {
	dsn := (&url.URL{Scheme: "file", Path: source}).String() + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("snapshot Crew database: %w", err)
	}
	return os.Chmod(destination, 0600)
}

func importCrewMoveDatabase(ctx context.Context, roots crewroot.Roots, source string) error {
	if source == "" {
		return nil
	}
	destination, err := crewMoveRecordPath(roots, "db/db.sqlite")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".move-db-*.sqlite")
	if err != nil {
		return err
	}
	stage := f.Name()
	f.Close()
	defer os.Remove(stage)
	if err = os.Remove(stage); err != nil {
		return err
	}
	// VACUUM validates the received image and creates a standalone database;
	// the destination is a new, inactive Crew until records are admitted.
	if err = snapshotCrewMoveDatabase(ctx, source, stage); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err = os.Remove(destination + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err = os.Rename(stage, destination); err != nil {
		return err
	}
	log.Printf("[CREW_SYNC] project=%s phase=database-imported", roots.ID)
	return nil
}
