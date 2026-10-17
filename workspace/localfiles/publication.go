package localfiles

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type PublicationSnapshot struct {
	ProjectID string `json:"project_id"`
	Size      int64  `json:"size"`
	Offset    int64  `json:"offset"`
	Data      []byte `json:"data,omitempty"`
}
type snapshotRecord struct {
	Archive   string    `json:"archive"`
	ProjectID string    `json:"project_id"`
	Created   time.Time `json:"created"`
}

var publicationSnapshotLocks sync.Map

func (e *Executor) publicationSnapshot(ctx context.Context, g Grant, r Request) (*PublicationSnapshot, error) {
	if !ValidID(r.RequestID) || len(r.RequestID) < 20 || r.Offset < 0 || r.Offset > publication.MaxBytes {
		return nil, fmt.Errorf("invalid publication receipt")
	}
	lock, _ := publicationSnapshotLocks.LoadOrStore(g.State+"/"+r.RequestID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	state := filepath.Join(g.State, "publications")
	if err := os.MkdirAll(state, 0700); err != nil {
		return nil, err
	}
	recordPath := filepath.Join(state, r.RequestID+".json")
	var record snapshotRecord
	if raw, err := os.ReadFile(recordPath); err == nil {
		if json.Unmarshal(raw, &record) != nil {
			return nil, fmt.Errorf("invalid snapshot receipt")
		}
	}
	if record.Archive == "" {
		if r.Offset != 0 {
			return nil, fmt.Errorf("snapshot expired; publish again")
		}
		var marker struct {
			ProjectID string `json:"project_id"`
		}
		if raw, err := e.roots[g.ID].ReadFile(".agentworks/publish.json"); err == nil {
			if json.Unmarshal(raw, &marker) != nil {
				return nil, fmt.Errorf("restore this project's publish identity before publishing")
			}
		}
		if marker.ProjectID == "" {
			marker.ProjectID = r.ProjectID
		}
		if marker.ProjectID == "" || len(marker.ProjectID) > 128 {
			return nil, fmt.Errorf("a stable project identity is required")
		}
		if g.Writable && g.Guard.Allows(".agentworks/publish.json", true) {
			if err := e.roots[g.ID].MkdirAll(".agentworks", 0700); err != nil {
				return nil, err
			}
			raw, _ := json.Marshal(marker)
			markerFile, err := e.roots[g.ID].OpenFile(".agentworks/publish.json", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				return nil, err
			}
			_, writeErr := markerFile.Write(raw)
			closeErr := markerFile.Close()
			if writeErr != nil {
				return nil, writeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
		ignored := publication.GitIgnored(ctx, g.Root)
		archive, _, err := publication.CaptureFiles(ctx, g.Root, state, marker.ProjectID, func(p string) bool { return g.Guard.Allows(p, false) && !ignored[p] })
		if err != nil {
			return nil, err
		}
		record = snapshotRecord{Archive: archive, ProjectID: marker.ProjectID, Created: time.Now()}
		raw, _ := json.Marshal(record)
		if err = os.WriteFile(recordPath, raw, 0600); err != nil {
			return nil, err
		}
	}
	if time.Since(record.Created) > publication.TransferTimeout {
		return nil, &wf.FileError{Status: 409, Message: "Snapshot expired. Publish again; the earlier hosted version is still live."}
	}
	file, err := os.Open(record.Archive)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	out := &PublicationSnapshot{ProjectID: record.ProjectID, Size: info.Size(), Offset: r.Offset}
	if r.Offset < info.Size() {
		out.Data = make([]byte, min(int64(publication.ChunkBytes), info.Size()-r.Offset))
		n, err := file.ReadAt(out.Data, r.Offset)
		if err != nil && err != io.EOF {
			return nil, err
		}
		out.Data = out.Data[:n]
	}
	return out, nil
}
