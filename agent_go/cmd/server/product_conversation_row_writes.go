package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
)

// Mutation callbacks only edit the supplied records. Resolve manifests, auth
// and remote services before entering these writer transactions.
func (r conversationRegistryRows) write(ctx context.Context, change func(*sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = change(tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO registry_versions(namespace,version) VALUES (?,?) ON CONFLICT(namespace) DO UPDATE SET version=excluded.version WHERE registry_versions.version!=excluded.version`, r.namespace, productConversationRegistryVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func (r conversationRegistryRows) currentIn(ctx context.Context, tx *sql.Tx, key string) (*ProductConversationRecord, string, error) {
	var payload string
	err := tx.QueryRowContext(ctx, `SELECT payload FROM registry_entries WHERE namespace=? AND entry_key=?`, r.namespace, key).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var record ProductConversationRecord
	if err = json.Unmarshal([]byte(payload), &record); err != nil {
		return nil, "", err
	}
	return &record, payload, nil
}

func (r conversationRegistryRows) saveCurrent(ctx context.Context, tx *sql.Tx, key, previous string, next *ProductConversationRecord) error {
	if next == nil {
		if previous == "" {
			return nil
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM registry_entries WHERE namespace=? AND entry_key=?`, r.namespace, key)
		return err
	}
	if previous != "" {
		var before ProductConversationRecord
		if err := json.Unmarshal([]byte(previous), &before); err != nil {
			return err
		}
		if reflect.DeepEqual(before, *next) {
			return nil
		}
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO registry_entries(namespace,entry_key,profile_id,session_id,workspace,payload) VALUES (?,?,?,?,?,?) ON CONFLICT(namespace,entry_key) DO UPDATE SET profile_id=excluded.profile_id,session_id=excluded.session_id,workspace=excluded.workspace,payload=excluded.payload`, r.namespace, key, next.ProfileID, next.SessionID, next.WorkspacePath, string(payload))
	return err
}

// Runtime binding and selected-step-run edits read/write one current row, not
// the chat's previous conversations or any other slot/user.
func (r conversationRegistryRows) mutateCurrent(ctx context.Context, profile, key string, change func(*ProductConversationRecord) (*ProductConversationRecord, error)) error {
	entry := productConversationRegistryEntryKey(profile, key)
	return r.write(ctx, func(tx *sql.Tx) error {
		current, payload, err := r.currentIn(ctx, tx, entry)
		if err != nil {
			return err
		}
		next, err := change(current)
		if err != nil {
			return err
		}
		return r.saveCurrent(ctx, tx, entry, payload, next)
	})
}

// Identity setters require a current slot, as in the existing JSON setters;
// an orphan identity remains readable but does not grant a new writable chat.
func (r conversationRegistryRows) mutateIdentity(ctx context.Context, profile, key string, change func(ProductAgentIdentity) (*ProductAgentIdentity, error)) error {
	entry := productConversationRegistryEntryKey(profile, key)
	return r.write(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM registry_entries WHERE namespace=? AND entry_key=?)`, r.namespace, entry).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errors.New("that agent no longer exists")
		}
		var payload string
		var identity ProductAgentIdentity
		err := tx.QueryRowContext(ctx, `SELECT payload FROM registry_agents WHERE namespace=? AND entry_key=?`, r.namespace, entry).Scan(&payload)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if err = json.Unmarshal([]byte(payload), &identity); err != nil {
				return err
			}
		}
		next, err := change(identity)
		if err != nil {
			return err
		}
		if next == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM registry_agents WHERE namespace=? AND entry_key=?`, r.namespace, entry)
			return err
		}
		if payload != "" {
			var before ProductAgentIdentity
			if err = json.Unmarshal([]byte(payload), &before); err != nil {
				return err
			}
			if reflect.DeepEqual(before, *next) {
				return nil
			}
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO registry_agents(namespace,entry_key,payload) VALUES (?,?,?) ON CONFLICT(namespace,entry_key) DO UPDATE SET payload=excluded.payload`, r.namespace, entry, string(encoded))
		return err
	})
}

// Rotation, reopen and forgetting history need one slot's ordered prior rows.
// Ordinary current/identity changes above never load them. The callback owns
// the existing rotation/retention rule; this storage layer never deduplicates
// or truncates an imported history silently.
func (r conversationRegistryRows) mutateSlot(ctx context.Context, profile, key string, change func(*ProductConversationRecord, []ProductConversationRecord) (*ProductConversationRecord, []ProductConversationRecord, error)) error {
	entry := productConversationRegistryEntryKey(profile, key)
	return r.write(ctx, func(tx *sql.Tx) error {
		current, payload, err := r.currentIn(ctx, tx, entry)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT payload FROM registry_previous WHERE namespace=? AND entry_key=? ORDER BY ordinal`, r.namespace, entry)
		if err != nil {
			return err
		}
		var previous []ProductConversationRecord
		var originalPayloads []string
		for rows.Next() {
			var raw string
			var record ProductConversationRecord
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			if err = json.Unmarshal([]byte(raw), &record); err != nil {
				rows.Close()
				return err
			}
			previous = append(previous, record)
			originalPayloads = append(originalPayloads, raw)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		next, prior, err := change(current, previous)
		if err != nil {
			return err
		}
		if err = r.saveCurrent(ctx, tx, entry, payload, next); err != nil {
			return err
		}
		unchanged := len(prior) == len(originalPayloads)
		if unchanged {
			for i, raw := range originalPayloads {
				var before ProductConversationRecord
				if err = json.Unmarshal([]byte(raw), &before); err != nil {
					return err
				}
				if !reflect.DeepEqual(before, prior[i]) {
					unchanged = false
					break
				}
			}
		}
		if unchanged {
			return nil
		}

		if _, err = tx.ExecContext(ctx, `DELETE FROM registry_previous WHERE namespace=? AND entry_key=?`, r.namespace, entry); err != nil {
			return err
		}
		for ordinal, record := range prior {
			raw, e := json.Marshal(record)
			if e != nil {
				return e
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO registry_previous(namespace,entry_key,ordinal,payload) VALUES (?,?,?,?)`, r.namespace, entry, ordinal, string(raw)); err != nil {
				return err
			}
		}
		return nil
	})
}
