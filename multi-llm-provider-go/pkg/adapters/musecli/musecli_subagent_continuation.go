package musecli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// museSubagentChain tracks, from Muse's own session log, whether a turn's
// native subagents have all been answered. Every subagent result is queued
// as an inbox item (source subagent_result) and later drained into a run:
// the same run when the parent waited, or a new inbox-delivery run when the
// parent ended its run first (background). Until every subagent spawned in
// the turn's chain of runs has been drained into a run that reached its own
// terminal, the chain's latest answer is interim.
type museSubagentChain struct {
	offset     int64
	minSeq     int64
	runs       map[string]bool   // runs belonging to this turn
	terminal   map[string]int64  // run -> terminal sequence (completed runs)
	spawned    map[string]string // subagent -> parent run (chain runs only)
	itemOf     map[string]string // inbox item -> subagent
	drainedTo  map[string]string // subagent -> run its result drained into
	failedRuns map[string]string // run -> non-completed terminal status
}

func newMuseSubagentChain(rootRun string, minSeq int64) *museSubagentChain {
	return &museSubagentChain{
		minSeq:     minSeq,
		runs:       map[string]bool{rootRun: true},
		terminal:   map[string]int64{},
		spawned:    map[string]string{},
		itemOf:     map[string]string{},
		drainedTo:  map[string]string{},
		failedRuns: map[string]string{},
	}
}

type museChainRow struct {
	Sequence    int64  `json:"sequence"`
	PayloadType string `json:"payload_type"`
	Payload     struct {
		Kind  string `json:"kind"`
		RunID string `json:"run_id"`
		// On inbox_item_queued this record ID is the inbox item ID that the
		// later inbox_item_drained row names (the queued row has no item_id).
		SourceRunRecordID string `json:"source_run_record_id"`
		Record            struct {
			Kind        string `json:"kind"`
			ParentRunID string `json:"parent_run_id"`
			SubagentID  string `json:"subagent_id"`
		} `json:"record"`
		Event struct {
			Kind     string `json:"kind"`
			Terminal string `json:"terminal"`
			ItemID   string `json:"item_id"`
			Source   struct {
				Source     string `json:"source"`
				SubagentID string `json:"subagent_id"`
			} `json:"source"`
			DrainTarget struct {
				ID string `json:"id"`
			} `json:"drain_target_run_stream"`
		} `json:"event"`
	} `json:"payload"`
}

// advance reads newly appended complete rows. Rows can reference runs that
// only join the chain later (a drain into a new run precedes nothing), so
// run-scoped facts are recorded for every run and filtered at evaluation.
func (c *museSubagentChain) advance(logPath string) error {
	f, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(c.offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		c.offset += int64(len(line))
		// A drain row can carry only the item ID (no "subagent"), so admit
		// inbox rows explicitly.
		if !bytes.Contains(line, []byte("subagent")) && !bytes.Contains(line, []byte("inbox_item")) && !bytes.Contains(line, []byte(`"terminal"`)) {
			continue
		}
		var row museChainRow
		if json.Unmarshal(line, &row) != nil || row.Sequence <= c.minSeq {
			continue
		}
		p := row.Payload
		switch {
		case row.PayloadType == "subagent.control.spawn_accepted" && p.Record.SubagentID != "":
			c.spawned[p.Record.SubagentID] = p.Record.ParentRunID
		case p.Kind == "run" && p.Event.Kind == "inbox_item_queued" && p.Event.Source.Source == "subagent_result":
			item := p.Event.ItemID
			if item == "" {
				item = p.SourceRunRecordID
			}
			c.itemOf[item] = p.Event.Source.SubagentID
		case p.Kind == "run" && p.Event.Kind == "inbox_item_drained":
			if sub := c.itemOf[p.Event.ItemID]; sub != "" {
				target := p.Event.DrainTarget.ID
				if target == "" {
					target = p.RunID
				}
				c.drainedTo[sub] = target
			}
		case p.Kind == "run" && p.Event.Kind == "terminal" && p.RunID != "":
			if p.Event.Terminal == "completed" {
				c.terminal[p.RunID] = row.Sequence
			} else {
				c.failedRuns[p.RunID] = p.Event.Terminal
			}
		}
	}
}

// state reports whether the chain still waits on a subagent, and the chain
// run whose terminal is latest (the one holding the final answer).
func (c *museSubagentChain) state() (pending bool, finalRun string, err error) {
	// Grow the chain to a fixed point: runs that received a chain subagent's
	// result join it, and subagents those runs spawn join it too.
	for changed := true; changed; {
		changed = false
		for sub, parent := range c.spawned {
			if !c.runs[parent] {
				continue
			}
			if target := c.drainedTo[sub]; target != "" && !c.runs[target] {
				c.runs[target] = true
				changed = true
			}
		}
	}
	for run := range c.runs {
		if status, failed := c.failedRuns[run]; failed {
			return false, "", fmt.Errorf("muse run %s ended %q", run, status)
		}
	}
	var best int64 = -1
	for run := range c.runs {
		if seq, ok := c.terminal[run]; ok && seq > best {
			best, finalRun = seq, run
		}
	}
	for sub, parent := range c.spawned {
		if !c.runs[parent] {
			continue
		}
		target := c.drainedTo[sub]
		if target == "" {
			return true, finalRun, nil // result not queued/drained yet
		}
		if _, done := c.terminal[target]; !done {
			return true, finalRun, nil // result delivered into a run still working
		}
	}
	return false, finalRun, nil
}

// museSubagentQuietFallback bounds a pending-subagent wait with no log growth.
const museSubagentQuietFallback = 5 * time.Minute

// museWaitSubagentContinuation runs after the accepted run's terminal. It
// returns the run holding the turn's final answer: rootRun when no subagent
// is outstanding, otherwise the inbox-delivery run that consumed the last
// background subagent result once that run completes.
func museWaitSubagentContinuation(ctx context.Context, session, logPath, rootRun string, minSeq int64) (string, error) {
	chain := newMuseSubagentChain(rootRun, minSeq)
	lastGrowth := time.Now()
	for {
		before := chain.offset
		if err := chain.advance(logPath); err != nil {
			return "", fmt.Errorf("read muse subagent chain: %w", err)
		}
		if chain.offset != before {
			lastGrowth = time.Now()
		}
		pending, finalRun, err := chain.state()
		if err != nil {
			return "", err
		}
		if !pending && finalRun != "" {
			return finalRun, nil
		}
		// Safety valve: should Muse's records change shape, a subagent that
		// never resolves must not hold the turn forever. A live subagent keeps
		// the parent log growing (status, reminders, results).
		if pending && finalRun != "" && time.Since(lastGrowth) >= museSubagentQuietFallback {
			return finalRun, nil
		}
		if !museTmuxSessionAlive(ctx, session) {
			return "", fmt.Errorf("muse tmux session %q died while background subagents were running", session)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
