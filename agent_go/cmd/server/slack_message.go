package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
)

type botExecutionSession struct {
	Claims  *UserClaims
	Request QueryRequest
}
type slackThreadReference struct {
	RouteID string       `json:"route_id"`
	Target  ChannelRoute `json:"target"`
	RootTS  string       `json:"root_ts"`
}
type slackSendRecord struct {
	Fingerprint string `json:"fingerprint"`
	Status      string `json:"status"`
	ChannelID   string `json:"channel_id"`
	MessageTS   string `json:"message_ts"`
	ThreadRef   string `json:"thread_ref"`
}

func (api *StreamingAPI) botExecutionForSession(session string) (botExecutionSession, bool) {
	seen := map[string]bool{}
	for session != "" && !seen[session] {
		seen[session] = true
		if value, ok := api.botExecutionSessions.Load(session); ok {
			return value.(botExecutionSession), true
		}
		if parent := virtualtools.GetParentChat(session); parent != nil && parent.SessionID != session {
			session = parent.SessionID
			continue
		}
		active, ok := api.getActiveSession(session)
		if !ok {
			break
		}
		session = active.ParentSessionID
	}
	return botExecutionSession{}, false
}

func slackReferencePath(ref string) (string, error) {
	if len(ref) != 64 {
		return "", fmt.Errorf("invalid thread_ref")
	}
	if _, err := hex.DecodeString(ref); err != nil {
		return "", fmt.Errorf("invalid thread_ref")
	}
	return "config/slack-threads/references/" + ref + ".json", nil
}
func saveSlackThreadReference(ctx context.Context, channel string, route ChannelRoute, rootTS string) (string, error) {
	// Random reference is opaque; the record is additionally target checked.
	sum := sha256.Sum256([]byte(uuid.NewString()))
	ref := fmt.Sprintf("%x", sum)
	path, _ := slackReferencePath(ref)
	raw, err := json.Marshal(slackThreadReference{RouteID: channel, Target: route, RootTS: rootTS})
	if err != nil {
		return "", err
	}
	return ref, writeFileToWorkspace(ctx, path, string(raw))
}

func (api *StreamingAPI) sendSlackMessageFromTool(ctx context.Context, args map[string]interface{}) (string, error) {
	session, _ := ctx.Value(common.ChatSessionIDKey).(string)
	if session == "" {
		session = mcpexecutor.SessionIDFromContext(ctx)
	}
	if session == "" {
		return "", fmt.Errorf("authenticated session required")
	}
	channel := stringFromRequestMap(args, "route_id")
	message := stringFromRequestMap(args, "message")
	key := stringFromRequestMap(args, "idempotency_key")
	ref := stringFromRequestMap(args, "thread_ref")
	if !slackChannelIDPattern.MatchString(channel) || message == "" || utf8.RuneCountInString(message) > 3000 || key == "" || len(key) > 200 {
		return "", fmt.Errorf("exact route_id, message (1–3000 characters), and idempotency_key (1–200 bytes) required")
	}
	cfg, routes, err := api.slackRoutes(ctx)
	if err != nil {
		return "", err
	}
	route, found, dedicated := api.slackToolRoute(ctx, session, channel, routes)
	if !slackTrafficAllowed(cfg, found, dedicated) || (route.BotGrant != "run" && route.BotGrant != "owner") {
		return "", fmt.Errorf("Slack route is inactive or revoked")
	}
	inheritedThread, err := api.authorizeSlackToolRoute(ctx, session, channel, route)
	if err != nil {
		return "", err
	}
	root := inheritedThread
	if ref != "" {
		path, err := slackReferencePath(ref)
		if err != nil {
			return "", err
		}
		raw, found, err := readFileFromWorkspace(ctx, path)
		if err != nil || !found {
			return "", fmt.Errorf("thread_ref unavailable")
		}
		var record slackThreadReference
		if json.Unmarshal([]byte(raw), &record) != nil || record.RouteID != channel || !sameSlackRouteDestination(record.Target, route) || record.RootTS == "" {
			return "", fmt.Errorf("thread_ref belongs to another route")
		}
		root = record.RootTS
	}
	targetID := servicesBotTargetID(route)
	digest := sha256.Sum256([]byte(targetID + "|" + channel + "|" + key))
	path := fmt.Sprintf("config/slack-outbox/%x.json", digest)
	fingerprint := sha256.Sum256([]byte(message + "|" + root))
	lock := scheduleRunFileLock(path)
	lock.Lock()
	defer lock.Unlock()
	raw, exists, err := readFileFromWorkspace(ctx, path)
	if err != nil {
		return "", err
	}
	if exists {
		var previous slackSendRecord
		if json.Unmarshal([]byte(raw), &previous) != nil {
			return "", fmt.Errorf("invalid send record")
		}
		if previous.Fingerprint != fmt.Sprintf("%x", fingerprint) {
			return "", fmt.Errorf("idempotency_key was already used for different content")
		}
		if previous.Status != "sent" {
			return "", fmt.Errorf("previous delivery outcome is uncertain; inspect Slack before attempting another send")
		}
		result, _ := json.Marshal(previous)
		return string(result), nil
	}
	record := slackSendRecord{Fingerprint: fmt.Sprintf("%x", fingerprint), Status: "pending", ChannelID: channel, ThreadRef: ref}
	persist := func() error {
		encoded, e := json.Marshal(record)
		if e != nil {
			return e
		}
		return writeFileToWorkspace(ctx, path, string(encoded))
	}
	if err := persist(); err != nil {
		return "", err
	}
	post := api.postSlackMessage
	if post == nil {
		svc, err := ensureSlackService()
		if err != nil {
			return "", fmt.Errorf("Slack connector unavailable")
		}
		target := svc
		if connID := slackToolConnectionID(ctx, api, session, route); connID != "" {
			target, err = svc.ServiceForConnection(connID)
			if err != nil {
				return "", fmt.Errorf("Slack connection unavailable")
			}
		}
		post = target.PostRouteMessage
	}
	ts, err := post(ctx, channel, root, message)
	if err != nil {
		return "", fmt.Errorf("Slack delivery failed; inspect the channel before retrying")
	}
	if root == "" {
		root = ts
	}
	if record.ThreadRef == "" {
		record.ThreadRef, err = saveSlackThreadReference(ctx, channel, route, root)
		if err != nil {
			return "", err
		}
	}
	record.Status = "sent"
	record.MessageTS = ts
	if err := persist(); err != nil {
		return "", err
	}
	result, err := json.Marshal(record)
	return string(result), err
}
func servicesBotTargetID(route ChannelRoute) string {
	return strings.Join([]string{route.WorkflowID, route.ProfileID, route.ConversationKey, route.WorkspacePath}, "|")
}

var slackCredentialCodecOnce sync.Once

func configureSlackCredentialCodec() {
	slackCredentialCodecOnce.Do(func() {
		services.ConfigureSlackCredentialCodec(func(value string) (string, error) { return encryptSecretValueWithAAD(value, []byte("operator:slack")) }, func(value string) (string, error) { return decryptSecretValueWithAAD(value, []byte("operator:slack")) })
	})
}

func (api *StreamingAPI) authorizeSlackToolRoute(ctx context.Context, session, channel string, route ChannelRoute) (string, error) {
	inheritedThread := ""
	if execution, ok := api.botExecutionForSession(session); ok {
		trusted := context.WithValue(ctx, UserContextKey, execution.Claims)
		if _, err := api.revalidateExecutionPrincipal(trusted, execution.Request); err != nil {
			return "", err
		}
		if channel != execution.Request.BotChannelID || !sameSlackRouteDestination(route, execution.Claims.ExecutionPrincipal.Target) {
			return "", fmt.Errorf("cross-route Slack send denied")
		}
		inheritedThread = execution.Request.BotThreadTS
	} else {
		if parent := virtualtools.GetParentChat(session); parent != nil {
			session = parent.SessionID
		}
		active, ok := api.getActiveSession(session)
		if !ok || active.WorkspacePath == "" {
			return "", fmt.Errorf("session target unavailable")
		}
		if normalizeConversationWorkspace(active.WorkspacePath) != normalizeConversationWorkspace(route.WorkspacePath) {
			return "", fmt.Errorf("route is outside this session target")
		}
		trusted := internalBotRequestContext(ctx, active.UserID)
		if route.WorkflowID != "" {
			access, manifest := workflowAccessForWorkspacePath(trusted, GetUserFromContext(trusted), route.WorkspacePath)
			if access == WorkflowAccessNone || manifest == nil || manifest.ID != route.WorkflowID {
				return "", fmt.Errorf("workflow route access denied")
			}
			if manifest.Kind == "relay" {
				return "", fmt.Errorf("Relays do not support Slack")
			}
		} else if _, err := requireSlackRouteProfileOwner(trusted, api, route); err != nil {
			return "", err
		}
	}
	return inheritedThread, nil
}
