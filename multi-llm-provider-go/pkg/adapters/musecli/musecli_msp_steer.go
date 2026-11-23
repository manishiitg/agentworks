package musecli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"io"
	"log"
	"os"
	"time"
)

// A turn/steer that reaches a turn in its last moments is lost by Muse: the
// host answers the request, the turn ends without reading the message, and the
// only trace is a turn_submit.rejected row (reason already_terminal) in the
// session journal just after the turn's terminal row, naming the steer's
// command ID. The message appears nowhere else in the journal (seen live on
// 6 Nov: a person's message showed as delivered and nothing answered it).
var (
	// museMSPSteerVerdictWait is how long, once every turn/steer has its answer,
	// the journal may take to show a rejection after the turn ended. The row
	// lands within about 50 ms of the terminal row.
	museMSPSteerVerdictWait = 2 * time.Second
	// museMSPSteerAnswerWait bounds the wait for a turn/steer still unanswered.
	museMSPSteerAnswerWait = 15 * time.Second
)

const museMSPSteerPoll = 50 * time.Millisecond

// museMSPSteer is one message handed to the running turn.
type museMSPSteer struct {
	commandID string
	message   string
	images    []llmtypes.NativeImageInput
	answered  bool
	// offset is the journal's size before the send: its verdict lies past it.
	offset int64
}

// museMSPTurnTotals is what the turns of one generation add up to.
type museMSPTurnTotals struct {
	answers    []string
	lastAnswer string
	input      int
	output     int
	cacheRead  int
	cacheWrite int
	model      string
	followups  int
}

func (s *museMSPSession) addSteerLocked(message string) *museMSPSteer {
	steer := &museMSPSteer{commandID: museCommandID(), message: message}
	if info, err := os.Stat(museSessionLogPath(s.sessionID, s.dataHome)); err == nil {
		steer.offset = info.Size()
	}
	s.steers = append(s.steers, steer)
	return steer
}

// finishSteerLocked records the host's answer. A refused steer was never taken
// and its caller reports that, so the turn's end has nothing to check.
func (s *museMSPSession) finishSteerLocked(steer *museMSPSteer, accepted bool) {
	steer.answered = true
	if accepted {
		return
	}
	for i, candidate := range s.steers {
		if candidate == steer {
			s.steers = append(s.steers[:i], s.steers[i+1:]...)
			return
		}
	}
}

func (s *museMSPSession) clearSteers() {
	s.mu.Lock()
	s.steers = nil
	s.mu.Unlock()
}

// museMSPMissedSteers returns, once, the messages the finished turn was given
// and Muse rejected. Only that rejection counts: a steer with none was read by
// the turn, and running it again would answer it twice. Host events arriving
// meanwhile are handled as between turns.
func (a *MuseCLIAdapter) museMSPMissedSteers(ctx context.Context, s *museMSPSession, events <-chan museRPCMessage) []*museMSPSteer {
	defer s.clearSteers()
	giveUp := time.Now().Add(museMSPSteerAnswerWait)
	deadline := time.Now().Add(museMSPSteerVerdictWait)
	for {
		s.mu.Lock()
		steers := append([]*museMSPSteer(nil), s.steers...)
		answered := true
		for _, steer := range steers {
			answered = answered && steer.answered
		}
		path := museSessionLogPath(s.sessionID, s.dataHome)
		s.mu.Unlock()
		if len(steers) == 0 {
			return nil
		}
		offset := steers[0].offset
		for _, steer := range steers {
			if steer.offset < offset {
				offset = steer.offset
			}
		}
		rejected, err := museRejectedCommands(path, offset, s.sessionID)
		var missed []*museMSPSteer
		for _, steer := range steers {
			if rejected[steer.commandID] {
				missed = append(missed, steer)
			}
		}
		now := time.Now()
		if !answered {
			deadline = now.Add(museMSPSteerVerdictWait)
		}
		if (answered && len(missed) == len(steers)) || now.After(deadline) || now.After(giveUp) {
			if err != nil {
				log.Printf("[MUSE_MSP] session=%s cannot read the journal for steer rejections: %v", s.sessionID, err)
			}
			return missed
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.rpc.done:
			return nil
		case msg := <-events:
			a.handleIdleMuseMSP(s, msg)
		case <-time.After(museMSPSteerPoll):
		}
	}
}

// museRejectedCommands lists the command IDs of the turn_submit.rejected rows
// at or after offset. A row still being written fails to parse and is read on
// the next poll.
func museRejectedCommands(path string, offset int64, session string) (map[string]bool, error) {
	if path == "" {
		return nil, errors.New("no session journal")
	}
	f, err := os.Open(path) //nolint:gosec // adapter-owned transcript path
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	rejected := map[string]bool{}
	// Rejection rows contain metadata/IDs only. Skip large native input rows
	// in bounded chunks rather than buffering image-bearing journal payloads.
	reader := bufio.NewReaderSize(f, 64*1024)
	oversized := false
	for {
		line, readErr := reader.ReadSlice('\n')
		if readErr == bufio.ErrBufferFull {
			oversized = true
			continue
		}
		if oversized {
			log.Printf("[MUSE_MSP] session=%s action=skipped reason=oversized_journal_row", session)
			oversized = false
		} else if bytes.Contains(line, []byte(`"turn_submit.rejected"`)) {
			var row struct {
				PayloadType string `json:"payload_type"`
				Payload     struct {
					Record struct {
						CommandID string `json:"command_id"`
					} `json:"record"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &row) == nil && row.PayloadType == "turn_submit.rejected" && row.Payload.Record.CommandID != "" {
				rejected[row.Payload.Record.CommandID] = true
			}
		}
		if readErr == io.EOF {
			return rejected, nil
		}
		if readErr != nil {
			return rejected, readErr
		}
	}
}
