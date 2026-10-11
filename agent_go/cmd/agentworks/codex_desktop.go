package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// This opt-in adapter uses the desktop's same-user coordination IPC. It never
// accesses the separately protected browser/app-tools pipe, guesses a chat, or
// changes permissions, models, the composer, or existing queued follow-ups.
func codexDesktopOffer(ctx context.Context, remote string, a agentworksclient.AgentAttachment, messages []agentworksclient.AgentLiveMessage) error {
	if _, err := uuid.Parse(a.SessionID); err != nil {
		return errors.New("desktop delivery requires an exact thread UUID")
	}
	remote = strings.TrimPrefix(remote, "desktop+")
	if err := validateCodexRemote(remote); err != nil {
		return err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", strings.TrimPrefix(remote, "unix://"))
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	client := "initializing-client"
	call := func(method string, params any, version int, target string) (map[string]json.RawMessage, error) {
		id := uuid.NewString()
		v := map[string]any{"type": "request", "requestId": id, "sourceClientId": client, "version": version, "method": method, "params": params, "timeoutMs": 10000}
		if target != "" {
			v["targetClientId"] = target
		}
		b, e := json.Marshal(v)
		if e != nil {
			return nil, e
		}
		header := make([]byte, 4)
		binary.LittleEndian.PutUint32(header, uint32(len(b)))
		if _, e = conn.Write(append(header, b...)); e != nil {
			return nil, e
		}
		for {
			if _, e = io.ReadFull(conn, header); e != nil {
				return nil, e
			}
			n := binary.LittleEndian.Uint32(header)
			if n == 0 || n > 8<<20 {
				return nil, errors.New("unsupported desktop IPC frame")
			}
			b = make([]byte, n)
			if _, e = io.ReadFull(conn, b); e != nil {
				return nil, e
			}
			var out map[string]json.RawMessage
			if e = json.Unmarshal(b, &out); e != nil {
				return nil, e
			}
			var rid, kind string
			_ = json.Unmarshal(out["requestId"], &rid)
			_ = json.Unmarshal(out["type"], &kind)
			if kind == "client-discovery-request" { // This adapter never owns a chat.
				reply, _ := json.Marshal(map[string]any{"type": "client-discovery-response", "requestId": rid, "response": map[string]any{"canHandle": false}})
				binary.LittleEndian.PutUint32(header, uint32(len(reply)))
				if _, e = conn.Write(append(header, reply...)); e != nil {
					return nil, e
				}
				continue
			}
			if kind != "response" || rid != id {
				continue
			}
			return out, nil
		}
	}
	out, err := call("initialize", map[string]any{"clientType": "agentworks"}, 0, "")
	if err != nil {
		return err
	}
	var init struct {
		ClientID string `json:"clientId"`
	}
	if json.Unmarshal(out["result"], &init) != nil || init.ClientID == "" {
		return errors.New("desktop IPC initialization refused")
	}
	client = init.ClientID
	out, err = call("thread-owner-discovery", map[string]any{"hostId": "local", "conversationId": a.SessionID}, 1, "")
	if err != nil {
		return err
	}
	var owner string
	var support struct {
		Untrusted bool `json:"supportsUntrustedAppInput"`
	}
	_ = json.Unmarshal(out["handledByClientId"], &owner)
	_ = json.Unmarshal(out["result"], &support)
	if owner == "" || !support.Untrusted {
		return errors.New("exact desktop chat unavailable or untrusted app input unsupported")
	}
	text := pluginMessageText(agentworksclient.LocalBridgeReply{Attached: true, Attachment: a, Messages: messages}, "codex_desktop")
	tool := map[string]any{"namespace": "agentworks", "name": "agent_message", "output": text}
	id := uuid.NewString()
	restore := map[string]any{"id": id, "context": map[string]any{"workspaceRoots": []string{}}}
	out, err = call("thread-follower-steer-turn", map[string]any{"conversationId": a.SessionID, "input": []any{}, "restoreMessage": restore, "clientUserMessageId": id, "toolOutput": tool}, 1, owner)
	if err != nil {
		return err
	}
	var result, errorText string
	_ = json.Unmarshal(out["resultType"], &result)
	_ = json.Unmarshal(out["error"], &errorText)
	if result == "success" {
		return nil
	}
	// Only a definite no-active-turn refusal permits an idle start. Timeouts,
	// disconnects and all other errors remain ambiguous and are never retried.
	if !strings.Contains(errorText, "no active turn") && !strings.Contains(errorText, "No active turn") && errorText != "Cannot steer conversation "+a.SessionID+" because its active turn already ended" {
		return errors.New("desktop refused live offer: " + errorText)
	}
	out, err = call("thread-follower-start-turn", map[string]any{"conversationId": a.SessionID, "turnStart": map[string]any{"request": map[string]any{"threadId": a.SessionID, "input": []any{}, "toolOutput": tool, "clientUserMessageId": id}, "context": map[string]any{"inheritThreadSettings": true}}}, 2, owner)
	if err != nil {
		return err
	}
	_ = json.Unmarshal(out["resultType"], &result)
	if result != "success" {
		return errors.New("desktop refused idle offer; inspect attachment status")
	}
	return nil
}
