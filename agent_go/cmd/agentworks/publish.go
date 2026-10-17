package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/cobra"
)

type publicationMarker struct {
	AgentID      string   `json:"agent_id,omitempty"`
	RuntimeSeeds []string `json:"runtime_seed_paths,omitempty"`
	ProjectID    string   `json:"project_id"`
	Server       string   `json:"server,omitempty"`
	WorkspaceID  string   `json:"workspace_id,omitempty"`
}
type pendingPublication struct {
	Root      string            `json:"root"`
	Archive   string            `json:"archive"`
	Operation string            `json:"operation"`
	Marker    publicationMarker `json:"marker"`
}

func publishCommand(o *options) *cobra.Command {
	var name, resume, crew, agentID string
	cmd := &cobra.Command{Use: "publish", Short: "Publish this project's files and this exact chat to a hosted Crew", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return runPublish(cmd.Context(), o, name, resume, crew, agentID)
	}}
	cmd.Flags().StringVar(&name, "name", "", "Crew name (default: project folder name)")
	cmd.Flags().StringVar(&crew, "crew", "", "Add an agent to this existing Crew (workspace ID from workspace list)")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "Republish this stable agent ID in the selected Crew")
	cmd.Flags().StringVar(&resume, "resume", "", "Resume this publication receipt after an interrupted upload")
	return cmd
}

func runPublish(ctx context.Context, o *options, name, resume, crew, agentID string) error {
	if err := o.useShareConfig(); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	cfg, _, err := o.connection(false)
	if err != nil {
		return err
	}
	client, err := o.client()
	if err != nil {
		return err
	}
	shareDir, err := o.shareDir()
	if err != nil {
		return err
	}
	state := filepath.Join(filepath.Dir(shareDir), "publications")
	if err = os.MkdirAll(state, 0700); err != nil {
		return err
	}
	pending := pendingPublication{Root: root}
	if resume != "" {
		if _, err = uuid.Parse(resume); err != nil {
			return fmt.Errorf("use the publication receipt returned by your previous publish")
		}
		b, e := os.ReadFile(filepath.Join(state, resume+".json"))
		if e != nil {
			return e
		}
		if json.Unmarshal(b, &pending) != nil || pending.Root != root || pending.Marker.Server != cfg.Server {
			return fmt.Errorf("this receipt belongs to a different project or server")
		}
	} else {
		grant, ok := runningShares(shareDir)[shareKey(root)]
		if !ok || !grant.Connected || grant.Folder != root || grant.Server != cfg.Server || !grant.Publication {
			return fmt.Errorf("Connect this project with the current AgentWorks app first: run agentworks start in this folder, then publish again. You can also publish its website chat from the project's Publish button.")
		}
		source, e := ownPublicationSource(ctx, o, root)
		if e != nil {
			return e
		}
		markerFile := filepath.Join(root, ".agentworks", "publish.json")
		if raw, e := os.ReadFile(markerFile); e == nil {
			if json.Unmarshal(raw, &pending.Marker) != nil || pending.Marker.ProjectID == "" {
				return fmt.Errorf("this project's publish identity is invalid; restore .agentworks/publish.json or publish a new project")
			}
		}
		if pending.Marker.ProjectID == "" {
			pending.Marker.ProjectID = uuid.NewString()
		}
		source.ProjectID = pending.Marker.ProjectID
		if name == "" {
			name = filepath.Base(root)
		}
		guard := wf.FolderGuard{ReadPaths: []string{"."}, BlockedPaths: grant.Blocked}
		ignored := publicationIgnored(ctx, root)
		archive, _, e := publication.Build(ctx, root, state, source, func(p string) bool { return guard.Allows(p, false) && !ignored[p] })
		if e != nil {
			return e
		}
		target := ""
		if pending.Marker.Server == cfg.Server {
			target = pending.Marker.WorkspaceID
			if agentID == "" {
				agentID = pending.Marker.AgentID
			}
		}
		if crew != "" {
			target = crew
			if crew != pending.Marker.WorkspaceID && agentID == pending.Marker.AgentID {
				agentID = ""
			}
		}
		receipt, e := client.Publication(ctx, http.MethodPost, "", map[string]any{"submission_id": uuid.NewString(), "project_id": source.ProjectID, "name": name, "workspace_id": target, "agent_id": agentID})
		if e != nil {
			_ = os.RemoveAll(filepath.Dir(archive))
			return e
		}
		pending.Archive = archive
		pending.Operation, _ = receipt["operation_id"].(string)
		pending.Marker.Server = cfg.Server
		if pending.Operation == "" {
			return fmt.Errorf("server returned no publication receipt")
		}
		if err = fsutil.WriteJSONAtomic(filepath.Join(state, pending.Operation+".json"), pending, 0600); err != nil {
			return err
		}
		// The identity travels with the project; raw history and the archive stay in private state.
		if err = fsutil.WriteJSONAtomic(markerFile, pending.Marker, 0600); err != nil {
			return err
		}
		account, _ := json.Marshal(receipt["account"])
		fmt.Fprintln(o.stderr, "Runs on the disclosed company account:", string(account))
		if target == "" {
			fmt.Fprintln(o.stderr, "New Crew starts Only me.")
		} else {
			fmt.Fprintln(o.stderr, "Existing Crew instructions and Share are preserved.")
		}
		fmt.Fprintln(o.stderr, "Publication receipt:", pending.Operation)
	}
	receipt, err := client.Publication(ctx, http.MethodGet, pending.Operation, nil)
	if err != nil {
		return err
	}
	if receipt["state"] == "uploading" || receipt["state"] == "failed" {
		f, e := os.Open(pending.Archive)
		if e != nil {
			return e
		}
		defer f.Close()
		offset, _ := receipt["uploaded_bytes"].(float64)
		if _, e = f.Seek(int64(offset), io.SeekStart); e != nil {
			return e
		}
		buf := make([]byte, publication.ChunkBytes)
		for {
			n, e := f.Read(buf)
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			_, err = client.Publication(ctx, http.MethodPut, pending.Operation, map[string]any{"offset": int64(offset), "data": buf[:n]})
			if err != nil {
				return fmt.Errorf("%w; resume with agentworks publish --resume %s", err, pending.Operation)
			}
			offset += float64(n)
		}
		receipt, err = client.Publication(ctx, http.MethodPost, pending.Operation, map[string]any{})
		if err != nil {
			return err
		}
	}
	for receipt["state"] == "preparing" {
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("publication continues on the server; check receipt %s", pending.Operation)
		case <-timer.C:
		}
		receipt, err = client.Publication(ctx, http.MethodGet, pending.Operation, nil)
		if err != nil {
			return err
		}
	}
	if receipt["state"] != "live" {
		return fmt.Errorf("publication %s: %v; retry with --resume %s", pending.Operation, receipt["error"], pending.Operation)
	}
	pending.Marker.WorkspaceID, _ = receipt["workspace_id"].(string)
	pending.Marker.AgentID, _ = receipt["agent_id"].(string)
	if pending.Marker.AgentID == "main" {
		pending.Marker.AgentID = ""
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(root, ".agentworks", "publish.json"), pending.Marker, 0600); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Dir(pending.Archive))
	_ = os.Remove(filepath.Join(state, pending.Operation+".json"))
	if !o.jsonOutput {
		fmt.Fprintln(o.stderr, "Published. Your Crew works while this computer is offline. Server chat continues from a private summary; native resume comes later.")
	}
	return o.output(receipt)
}
