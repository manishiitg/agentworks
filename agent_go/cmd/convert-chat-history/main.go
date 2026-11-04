// Command convert-chat-history moves Crew chats and AgentWorks home chats to
// the chat folder format (PLAT-856-521), once per server:
//
//	<store>/<YYYY-MM-DD>/session-<id>-conversation.json   (one copy per day used)
//	  ->  <store>/session-<id>/header.json + turns.jsonl
//
// A store is a Crew's builder/conversation folder, a Crew's reader copies
// (builder/crew-chats/users/<reader>), or a person's chat_history folder.
// Goals chats (anything under a Workflow folder) are left untouched.
//
// For each chat it reads every dated copy and merges them: the union of their
// turns in order (a past restart cut one chat down, and the cut copy then grew
// turns the older copy lacks, so no single copy is complete). When copies hold
// the same turn, the fullest version is kept. Each merged chat is listed with
// how many turns came from which copy, flagged DISAGREE when copies differ on
// a turn's content. One turn line is written per person message. Old files are
// never deleted or changed: back them up, check the result, then remove them.
//
// --reconvert rebuilds chats that were already converted from those dated
// files; the old chat folder is moved aside to <store>-reconvert-backup/<time>/
// (outside the store) and turns added since the first conversion are kept.
//
// Dry run (default) prints the report only. --apply writes the chat folders;
// stop the AgentWorks server first so no chat is saved while it runs.
//
//	go run ./cmd/convert-chat-history --root /path/to/workspace-docs
//	go run ./cmd/convert-chat-history --root /path/to/workspace-docs --apply --app-stopped
//	go run ./cmd/convert-chat-history --root /path/to/workspace-docs --reconvert --apply --app-stopped
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
)

type rootsFlag []string

func (r *rootsFlag) String() string     { return strings.Join(*r, ",") }
func (r *rootsFlag) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	var roots rootsFlag
	flag.Var(&roots, "root", "folder to scan (the workspace docs root; repeat for local Crew folders)")
	apply := flag.Bool("apply", false, "write the chat folders (default: dry run)")
	appStopped := flag.Bool("app-stopped", false, "confirm the AgentWorks server is stopped (required with --apply)")
	reconvert := flag.Bool("reconvert", false, "rebuild already converted chats from their dated files (the old chat folder is moved aside; turns added since are kept)")
	flag.Parse()
	if len(roots) == 0 {
		fmt.Fprintln(os.Stderr, "usage: convert-chat-history --root <workspace-docs> [--root <more>] [--apply --app-stopped]")
		os.Exit(2)
	}
	if *apply && !*appStopped {
		fmt.Fprintln(os.Stderr, "refusing --apply: stop the AgentWorks server first, then add --app-stopped")
		os.Exit(2)
	}
	report, err := run(roots, *apply, *reconvert, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if report.Failures > 0 {
		os.Exit(1)
	}
}

// copyFile is one dated copy of a chat.
type copyFile struct {
	path      string
	day       string
	modTime   time.Time
	size      int64
	userTurns int
	record    []byte
}

type chatKey struct{ store, session string }

type Report struct {
	Chats, Converted, Skipped, Failures, Turns int
	BytesBefore, BytesAfter                    int64
	Merged, Disagreeing                        int
}

func run(roots []string, apply, reconvert bool, out io.Writer) (Report, error) {
	var report Report
	chats := map[chatKey][]copyFile{}
	for _, root := range roots {
		root = filepath.Clean(root)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					return nil
				}
				return err
			}
			if entry.IsDir() {
				// Goals chats stay as they are.
				if entry.Name() == "Workflow" || entry.Name() == ".git" || entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			store, session, day, ok := classify(root, path)
			if !ok {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			key := chatKey{store, session}
			chats[key] = append(chats[key], copyFile{path: path, day: day, modTime: info.ModTime()})
			return nil
		})
		if err != nil {
			return report, err
		}
	}
	keys := make([]chatKey, 0, len(chats))
	for key := range chats {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].store != keys[j].store {
			return keys[i].store < keys[j].store
		}
		return keys[i].session < keys[j].session
	})
	mode := "DRY RUN"
	if apply {
		mode = "APPLY"
	}
	if reconvert {
		mode += " RECONVERT"
	}
	fmt.Fprintf(out, "convert-chat-history %s: %d chats found\n", mode, len(keys))
	for _, key := range keys {
		report.Chats++
		copies := chats[key]
		for i := range copies {
			record, err := chatlog.Load(copies[i].path)
			if err != nil {
				fmt.Fprintf(out, "FAIL  %s: read %s: %v\n", key.store, copies[i].path, err)
				report.Failures++
				continue
			}
			copies[i].record = record
			copies[i].size = int64(len(record))
			plan, err := chatlog.PlanRecord(record, "converted")
			if err == nil {
				copies[i].userTurns = plan.UserTurns
			}
			report.BytesBefore += fileBytes(copies[i].path)
		}
		// Every copy is merged: one copy can hold turns another lacks (a
		// restart cut a chat down, and the cut copy then grew new turns).
		sort.SliceStable(copies, func(i, j int) bool {
			if copies[i].day != copies[j].day {
				return copies[i].day < copies[j].day
			}
			return copies[i].modTime.Before(copies[j].modTime)
		})
		var plans []*chatlog.Plan
		var used []copyFile
		for _, c := range copies {
			if c.record == nil {
				continue
			}
			plan, err := chatlog.PlanRecord(c.record, "converted")
			if err != nil {
				report.Failures++
				fmt.Fprintf(out, "FAIL  %s: plan %s: %v\n", key.store, c.path, err)
				continue
			}
			plans = append(plans, plan)
			used = append(used, c)
		}
		if len(plans) == 0 {
			continue
		}
		plan, merge := chatlog.MergePlans(plans, "converted")
		if len(plans) > 1 {
			report.Merged++
			var parts []string
			for i, n := range merge.FromCopy {
				parts = append(parts, fmt.Sprintf("%s:%d/%d", used[i].day, n, used[i].userTurns))
			}
			flag := ""
			if merge.Disagree > 0 {
				report.Disagreeing++
				flag = fmt.Sprintf(" DISAGREE=%d", merge.Disagree)
			}
			fmt.Fprintf(out, "MERGED %s session=%s turns=%d kept_from(day:kept/person_turns)=%s%s\n", key.store, key.session, len(plan.Turns), strings.Join(parts, ","), flag)
		}
		dir := filepath.Join(key.store, "session-"+key.session)
		var err error
		var keep []chatlog.Turn
		exists := false
		if info, statErr := os.Stat(filepath.Join(dir, chatlog.TurnsFile)); statErr == nil && info.Size() > 0 {
			exists = true
		}
		if exists && reconvert {
			// Rebuild from the dated files, keeping turns added since the
			// last conversion (every line not written by the converter).
			if chat, readErr := chatlog.ReadChat(dir, 0); readErr != nil {
				err = readErr
			} else {
				for _, turn := range chat.Turns {
					if !strings.HasPrefix(turn.ID, "converted-") {
						keep = append(keep, turn)
					}
				}
				for key, value := range chat.Header {
					if key == "title" || key == "runtime" || key == "terminal_snapshots" || key == "updated_at" {
						plan.Header[key] = value
					}
				}
			}
		} else if exists {
			err = chatlog.ErrAlreadyConverted
		}
		if err == nil && apply {
			if exists && reconvert {
				// Outside the chat store, so it is never listed as a chat.
				backup := filepath.Join(filepath.Dir(key.store), filepath.Base(key.store)+"-reconvert-backup", time.Now().UTC().Format("20060102T150405Z"), filepath.Base(dir))
				if err = os.MkdirAll(filepath.Dir(backup), 0o755); err == nil {
					err = os.Rename(dir, backup)
				}
				if err == nil {
					fmt.Fprintf(out, "MOVED %s to %s\n", dir, backup)
				}
			}
			if err == nil {
				err = chatlog.WritePlan(dir, plan, keep)
			}
		}
		switch {
		case errors.Is(err, chatlog.ErrAlreadyConverted):
			report.Skipped++
			fmt.Fprintf(out, "SKIP  %s session=%s: already converted (--reconvert rebuilds it)\n", key.store, key.session)
			continue
		case err != nil:
			report.Failures++
			fmt.Fprintf(out, "FAIL  %s session=%s: %v\n", key.store, key.session, err)
			continue
		}
		pick := used[len(used)-1]
		report.Converted++
		report.Turns += len(plan.Turns)
		report.BytesAfter += plan.Bytes
		fmt.Fprintf(out, "OK    %s session=%s copies=%d turns=%d kept_new_turns=%d messages=%d bytes_before=%d bytes_after=%d newest=%s\n",
			key.store, key.session, len(used), len(plan.Turns), len(keep), plan.Messages, pick.size, plan.Bytes, filepath.Base(filepath.Dir(pick.path)))
	}
	fmt.Fprintf(out, "SUMMARY chats=%d converted=%d skipped=%d failures=%d turns=%d merged=%d disagreeing=%d bytes_before=%d bytes_after=%d\n",
		report.Chats, report.Converted, report.Skipped, report.Failures, report.Turns, report.Merged, report.Disagreeing, report.BytesBefore, report.BytesAfter)
	return report, nil
}

func fileBytes(path string) int64 {
	var total int64
	for _, p := range []string{path, chatlog.HistoryLogPath(path)} {
		if info, err := os.Stat(p); err == nil {
			total += info.Size()
		}
	}
	return total
}

// classify reports whether path is a dated Crew or home chat copy, and its
// store folder, session id and day.
//
//	<crew>/builder/conversation/<day>/session-<id>-conversation.json
//	<crew>/builder/crew-chats/users/<reader>/session-<id>-conversation.json
//	<user>/chat_history/<day>/session-<id>-conversation.json
func classify(root, path string) (store, session, day string, ok bool) {
	name := filepath.Base(path)
	if !strings.HasPrefix(name, "session-") || !strings.HasSuffix(name, "-conversation.json") {
		return "", "", "", false
	}
	session = strings.TrimSuffix(strings.TrimPrefix(name, "session-"), "-conversation.json")
	if session == "" {
		return "", "", "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", "", "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts {
		if part == "Workflow" {
			return "", "", "", false
		}
	}
	parent := filepath.Dir(path)
	grand := filepath.Base(filepath.Dir(parent))
	if _, err := time.Parse("2006-01-02", filepath.Base(parent)); err == nil {
		if grand == "chat_history" || (grand == "conversation" && filepath.Base(filepath.Dir(filepath.Dir(parent))) == "builder") {
			return filepath.Dir(parent), session, filepath.Base(parent), true
		}
		return "", "", "", false
	}
	if grand == "users" && filepath.Base(filepath.Dir(filepath.Dir(parent))) == "crew-chats" {
		return parent, session, "", true
	}
	return "", "", "", false
}
