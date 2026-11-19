package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	_ "modernc.org/sqlite"
)

// The features store (#3210): one SQLite file per project, next to its Team record. A change writes only its own
// rows, and a story line is one insert, so a long story costs nothing to the next change. With the Team on, the
// project's file is on the server, where its Team is; a Mac keeps one only for a Crew whose Team is off, and hands it
// to the Team once the Team is on (crewFeaturesHandOver).

const crewFeaturesFile = "features.sqlite"

// A fixed-width time sorts as text.
const crewFeatureTimeFormat = "2006-01-02T15:04:05.000000000Z"

var crewFeaturesSchema = []string{
	`CREATE TABLE IF NOT EXISTS features (id TEXT PRIMARY KEY, name TEXT NOT NULL, name_key TEXT NOT NULL UNIQUE, link TEXT NOT NULL DEFAULT '', link_key TEXT NOT NULL DEFAULT '', done INTEGER NOT NULL DEFAULT 0, created_by TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, changed_at TEXT NOT NULL)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS features_link ON features(link_key) WHERE link_key <> ''`,
	`CREATE TABLE IF NOT EXISTS feature_agents (feature_id TEXT NOT NULL REFERENCES features(id) ON DELETE CASCADE, chat_key TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', joined_at TEXT NOT NULL, PRIMARY KEY(feature_id, chat_key))`,
	`CREATE TABLE IF NOT EXISTS feature_story (feature_id TEXT NOT NULL REFERENCES features(id) ON DELETE CASCADE, id TEXT NOT NULL, at TEXT NOT NULL, kind TEXT NOT NULL, by_name TEXT NOT NULL DEFAULT '', text TEXT NOT NULL, why TEXT NOT NULL DEFAULT '', proof TEXT NOT NULL DEFAULT '', task TEXT NOT NULL DEFAULT '', owner TEXT NOT NULL DEFAULT '', PRIMARY KEY(feature_id, id))`,
	`CREATE INDEX IF NOT EXISTS feature_story_at ON feature_story(feature_id, at)`,
}

// Open stores stay open: a project's file is read on every task list.
var crewFeatureDBs = struct {
	sync.Mutex
	open map[string]*sql.DB
}{open: map[string]*sql.DB{}}

func crewFeaturesPath(project string) (string, error) {
	dir, err := crewTeamDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, crewFeaturesFile), nil
}

// openCrewFeatures is the project's store. Without create, a project that never had a feature has no store (nil).
func openCrewFeatures(project string, create bool) (*sql.DB, error) {
	path, err := crewFeaturesPath(project)
	if err != nil {
		return nil, err
	}
	crewFeatureDBs.Lock()
	defer crewFeatureDBs.Unlock()
	if db := crewFeatureDBs.open[path]; db != nil {
		return db, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) && !create {
		return nil, nil
	}
	if err := agentworksclient.EnsurePrivateDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(path)+"&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range crewFeaturesSchema {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("features store: %w", err)
		}
	}
	_ = os.Chmod(path, 0o600)
	crewFeatureDBs.open[path] = db
	return db, nil
}

// closeCrewFeatures closes the project's store so its file can be moved.
func closeCrewFeatures(path string) {
	crewFeatureDBs.Lock()
	defer crewFeatureDBs.Unlock()
	if db := crewFeatureDBs.open[path]; db != nil {
		db.Close()
		delete(crewFeatureDBs.open, path)
	}
}

func crewFeatureTime(t time.Time) string { return t.UTC().Format(crewFeatureTimeFormat) }

func crewFeatureParseTime(s string) time.Time {
	t, _ := time.Parse(crewFeatureTimeFormat, s)
	return t
}

// sqlQuerier is a store or one of its transactions.
type sqlQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// crewFeaturesLoad is every feature with its joined agents, without the story.
func crewFeaturesLoad(ctx context.Context, q sqlQuerier) ([]crewFeature, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, link, done, created_by, created_at, changed_at FROM features ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	features := []crewFeature{}
	at := map[string]int{}
	for rows.Next() {
		var f crewFeature
		var created, changed string
		if err := rows.Scan(&f.ID, &f.Name, &f.Link, &f.Done, &f.CreatedBy, &created, &changed); err != nil {
			rows.Close()
			return nil, err
		}
		f.CreatedAt, f.ChangedAt = crewFeatureParseTime(created), crewFeatureParseTime(changed)
		at[f.ID] = len(features)
		features = append(features, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	agents, err := q.QueryContext(ctx, `SELECT feature_id, chat_key, name FROM feature_agents ORDER BY joined_at, chat_key`)
	if err != nil {
		return nil, err
	}
	defer agents.Close()
	for agents.Next() {
		var id, key, name string
		if err := agents.Scan(&id, &key, &name); err != nil {
			return nil, err
		}
		i, ok := at[id]
		if !ok {
			continue
		}
		f := &features[i]
		f.Agents = append(f.Agents, key)
		if name != "" {
			if f.Names == nil {
				f.Names = map[string]string{}
			}
			f.Names[key] = name
		}
	}
	return features, agents.Err()
}

func readCrewFeatures(project string) []crewFeature {
	db, err := openCrewFeatures(project, false)
	if err != nil || db == nil {
		return nil
	}
	features, err := crewFeaturesLoad(context.Background(), db)
	if err != nil {
		log.Printf("[CREW_FEATURES] project=%s event=read_failed reason=%q", project, err)
		return nil
	}
	return features
}

// crewFeatureStory is a feature's story, newest first, without lines only another person may see. kind "" is
// every kind; limit 0 is all of it. viewer "*" is everyone's (the handover).
func crewFeatureStory(project, feature, viewer, kind string, limit int) []crewFeatureEvent {
	db, err := openCrewFeatures(project, false)
	if err != nil || db == nil {
		return []crewFeatureEvent{}
	}
	query := `SELECT id, at, kind, by_name, text, why, proof, task, owner FROM feature_story WHERE feature_id = ? AND (? = '*' OR owner = '' OR owner = ?) AND (? = '' OR kind = ?) ORDER BY at DESC, id`
	args := []any{feature, viewer, viewer, kind, kind}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return []crewFeatureEvent{}
	}
	defer rows.Close()
	story := []crewFeatureEvent{}
	for rows.Next() {
		var e crewFeatureEvent
		var at string
		if rows.Scan(&e.ID, &at, &e.Kind, &e.By, &e.Text, &e.Why, &e.Proof, &e.Task, &e.Owner) == nil {
			e.At = crewFeatureParseTime(at)
			story = append(story, e)
		}
	}
	return story
}

// crewFeatureStoryHas is which of ids are already in the feature's story.
func crewFeatureStoryHas(db *sql.DB, feature string) map[string]bool {
	have := map[string]bool{}
	rows, err := db.Query(`SELECT id FROM feature_story WHERE feature_id = ?`, feature)
	if err != nil {
		return have
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			have[id] = true
		}
	}
	return have
}

// crewFeatureWrite runs change in one write transaction of the project's store, creating the store if needed.
func crewFeatureWrite(project string, change func(*sql.Tx) error) error {
	db, err := openCrewFeatures(project, true)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := change(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func crewFeatureInsert(tx *sql.Tx, f crewFeature) error {
	_, err := tx.Exec(`INSERT INTO features (id, name, name_key, link, link_key, done, created_by, created_at, changed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.Name, strings.ToLower(f.Name), f.Link, strings.ToLower(f.Link), f.Done, f.CreatedBy, crewFeatureTime(f.CreatedAt), crewFeatureTime(f.ChangedAt))
	return err
}

func crewFeatureSave(tx *sql.Tx, f crewFeature) error {
	_, err := tx.Exec(`UPDATE features SET link = ?, link_key = ?, done = ?, changed_at = ? WHERE id = ?`, f.Link, strings.ToLower(f.Link), f.Done, crewFeatureTime(f.ChangedAt), f.ID)
	return err
}

func crewFeatureAddAgent(tx *sql.Tx, feature, chat, name string, at time.Time) (bool, error) {
	res, err := tx.Exec(`INSERT OR IGNORE INTO feature_agents (feature_id, chat_key, name, joined_at) VALUES (?, ?, ?, ?)`, feature, chat, name, crewFeatureTime(at))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// crewFeatureAddEvent adds a story line once: a line with the same id is kept as it was.
func crewFeatureAddEvent(q sqlQuerier, feature string, e crewFeatureEvent) error {
	_, err := q.ExecContext(context.Background(), `INSERT OR IGNORE INTO feature_story (feature_id, id, at, kind, by_name, text, why, proof, task, owner) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		feature, e.ID, crewFeatureTime(e.At), e.Kind, e.By, e.Text, e.Why, e.Proof, e.Task, e.Owner)
	return err
}

// crewFeatureHandOff is what a Mac sends the Team when its Crew's Team is on: every feature, its agents and its whole
// story (each line keeps who alone may see it).
type crewFeatureHandOff struct {
	LocalProject string        `json:"local_project"`
	Features     []crewFeature `json:"features"`
}

// crewFeaturesMerge adds handed-over features to the project's store: a feature with the same name, or the same link,
// is the same feature; agents and story lines already there are kept. Chat keys of the Mac's own chats are renamed
// from its Crew to the Team's, as the Team knows them.
func crewFeaturesMerge(project string, in crewFeatureHandOff) (int, error) {
	added := 0
	err := crewFeatureWrite(project, func(tx *sql.Tx) error {
		have, err := crewFeaturesLoad(context.Background(), tx)
		if err != nil {
			return err
		}
		for _, f := range in.Features {
			f.Name, f.Link = agentTaskOneLine(f.Name), crewFeatureLink(f.Link)
			if f.Name == "" || len([]rune(f.Name)) > crewFeatureNameMax || len([]rune(f.Link)) > crewFeatureLinkMax {
				continue
			}
			at := crewFeatureFind(have, f.Name)
			if at < 0 && f.Link != "" {
				at = crewFeatureByLink(have, f.Link)
			}
			id := f.ID
			if at >= 0 {
				id = have[at].ID
				if have[at].Link == "" && f.Link != "" && crewFeatureByLink(have, f.Link) < 0 {
					have[at].Link = f.Link
					if err := crewFeatureSave(tx, have[at]); err != nil {
						return err
					}
				}
			} else {
				if len(have) >= crewFeaturesMax {
					continue
				}
				if crewFeatureFind(have, id) >= 0 || !strings.HasPrefix(id, "feat-") {
					id = crewFeatureNewID()
				}
				if crewFeatureByLink(have, f.Link) >= 0 {
					f.Link = ""
				}
				f.ID = id
				if err := crewFeatureInsert(tx, f); err != nil {
					return err
				}
				have = append(have, f)
				added++
			}
			for _, mac := range f.Agents {
				key := mac
				if in.LocalProject != "" && strings.HasPrefix(key, in.LocalProject+codeChatSideMarker) {
					key = project + strings.TrimPrefix(key, in.LocalProject)
				}
				if _, err := crewFeatureAddAgent(tx, id, key, f.Names[mac], f.ChangedAt); err != nil {
					return err
				}
			}
			for _, e := range f.Story {
				if e.ID == "" || e.Text == "" {
					continue
				}
				if err := crewFeatureAddEvent(tx, id, e); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return added, err
}

// handleCrewFeaturesHandOff takes the features the owner's Mac kept while its Crew's Team was off.
func (api *StreamingAPI) handleCrewFeaturesHandOff(w http.ResponseWriter, r *http.Request) {
	team, claims, ok := crewTeamForTab(w, r, true)
	if !ok {
		return
	}
	if !team.On || claims.UserID != team.OwnerID {
		externalError(w, 403, "features_forbidden", "Only the Crew's owner hands its features to its Team, once the Team is on")
		return
	}
	var in crewFeatureHandOff
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&in); err != nil {
		externalError(w, 400, "invalid_features", "Send the features to hand over")
		return
	}
	added, err := crewFeaturesMerge(team.ProjectID, in)
	if err != nil {
		externalError(w, 500, "features_unavailable", err.Error())
		return
	}
	log.Printf("[CREW_FEATURES] project=%s event=received from=%s features=%d new=%d", team.ProjectID, in.LocalProject, len(in.Features), added)
	externalJSON(w, map[string]any{"features": len(in.Features), "new": added})
}

// crewFeaturesHandOver: once a Mac Crew's Team is on, the features this Mac kept for it go to the Team, and the
// Mac's file is set aside (kept as features.sqlite.handed-over). A failed send is tried again at the next check.
func (api *StreamingAPI) crewFeaturesHandOver(ctx context.Context, owner string, c desktopConnection, local, server string) {
	path, err := crewFeaturesPath(local)
	if err != nil {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	in := crewFeatureHandOff{LocalProject: local, Features: readCrewFeatures(local)}
	events := 0
	for i := range in.Features {
		in.Features[i].Story = crewFeatureStory(local, in.Features[i].ID, "*", "", 0)
		events += len(in.Features[i].Story)
	}
	if len(in.Features) > 0 {
		send, stop := context.WithTimeout(ctx, 60*time.Second)
		err = crewTeamRemoteJSON(send, owner, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(server)+"/features/hand-off", in, nil)
		stop()
		if err != nil {
			log.Printf("[CREW_FEATURES] project=%s team=%s event=hand_over_failed reason=%q", local, server, err)
			return
		}
	}
	closeCrewFeatures(path)
	aside := path + ".handed-over"
	_ = os.Remove(aside)
	if err := os.Rename(path, aside); err != nil {
		log.Printf("[CREW_FEATURES] project=%s event=set_aside_failed reason=%q", local, err)
		return
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	log.Printf("[CREW_FEATURES] project=%s team=%s event=handed_over features=%d events=%d", local, server, len(in.Features), events)
}
