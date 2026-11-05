package server

import (
	"context"
	"io"
	"log"
	"sync"
	"time"
)

// A shared folder's sync round in progress (owner, 5 Nov: "show some kind of progress bar.. how many files / size is
// synced"). Each round sends or fetches one bundle of known size, so the client knows the files in the round and the
// bytes done of the bundle; it does not know files done until the bundle has arrived, so the screen says "Sending
// 342 files · 18 of 54 MB". Kept in memory: a restart starts the round again and its numbers with it.

type crewTeamRound struct {
	Direction  string    `json:"direction"` // upload or download
	Files      int       `json:"files"`
	BytesDone  int64     `json:"bytes_done"`
	BytesTotal int64     `json:"bytes_total"`
	Started    time.Time `json:"started"`
	project    string
}

var crewTeamRounds sync.Map // binding dir -> *crewTeamRound (guarded by crewTeamRoundsMu for updates)
var crewTeamRoundsMu sync.Mutex

type crewTeamRoundKey struct{}

func startCrewTeamRound(ctx context.Context, dir, project, direction string, files int, total, done int64) context.Context {
	crewTeamRounds.Store(dir, &crewTeamRound{Direction: direction, Files: files, BytesDone: done, BytesTotal: total, Started: time.Now(), project: project})
	log.Printf("[CREW_TEAM] project=%s round=%s event=started files=0/%d bytes=%d/%d", project, direction, files, done, total)
	publishCrewPanelRoundChanged(dir)
	return context.WithValue(ctx, crewTeamRoundKey{}, dir)
}

// crewTeamRoundTotal sets how many bytes this round moves, once that is known (an upload asks the server first).
func crewTeamRoundTotal(ctx context.Context, total int64) {
	dir, _ := ctx.Value(crewTeamRoundKey{}).(string)
	if dir == "" {
		return
	}
	crewTeamRoundsMu.Lock()
	defer crewTeamRoundsMu.Unlock()
	if v, ok := crewTeamRounds.Load(dir); ok {
		round := *v.(*crewTeamRound)
		round.BytesTotal = total
		crewTeamRounds.Store(dir, &round)
		publishCrewPanelRoundChanged(dir)
	}
}

func crewTeamRoundBytes(ctx context.Context, done int64) {
	dir, _ := ctx.Value(crewTeamRoundKey{}).(string)
	if dir == "" {
		return
	}
	crewTeamRoundsMu.Lock()
	defer crewTeamRoundsMu.Unlock()
	if v, ok := crewTeamRounds.Load(dir); ok {
		round := *v.(*crewTeamRound)
		round.BytesDone = done
		crewTeamRounds.Store(dir, &round)
		publishCrewPanelRoundChanged(dir)
	}
}

func endCrewTeamRound(dir string, err error) {
	v, ok := crewTeamRounds.LoadAndDelete(dir)
	if !ok {
		return
	}
	round := v.(*crewTeamRound)
	publishCrewPanelRoundChanged(dir)
	event, files := "done", round.Files
	if err != nil {
		event, files = "stopped", 0
	}
	log.Printf("[CREW_TEAM] project=%s round=%s event=%s files=%d/%d bytes=%d/%d seconds=%.0f", round.project, round.Direction, event, files, round.Files, round.BytesDone, round.BytesTotal, time.Since(round.Started).Seconds())
}

func crewTeamRoundFor(dir string) *crewTeamRound {
	if v, ok := crewTeamRounds.Load(dir); ok {
		round := *v.(*crewTeamRound)
		return &round
	}
	return nil
}

// crewTeamCountingReader reports a download's bytes as they arrive.
type crewTeamCountingReader struct {
	ctx  context.Context
	r    io.Reader
	done int64
	last time.Time
}

func (c *crewTeamCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if time.Since(c.last) > 250*time.Millisecond || err != nil {
		c.last = time.Now()
		crewTeamRoundBytes(c.ctx, c.done)
	}
	return n, err
}
