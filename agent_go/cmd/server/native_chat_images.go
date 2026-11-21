package server

import (
	"context"
	"errors"
	"fmt"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// Only the upload writer can issue these references. They are private queue
// principal data, never accepted from request JSON or inferred from typed paths.
type admittedChatAttachment struct {
	User         string `json:"user"`
	Session      string `json:"session"`
	Project      string `json:"project"`
	Conversation string `json:"conversation"`
	Hash         string `json:"hash"`
	Path         string `json:"path"`
	Bytes        int64  `json:"bytes"`
}
type admittedChatAttachmentsKey struct{}

func admittedChatAttachmentsFromContext(ctx context.Context) []admittedChatAttachment {
	refs, _ := ctx.Value(admittedChatAttachmentsKey{}).([]admittedChatAttachment)
	return append([]admittedChatAttachment(nil), refs...)
}
func withAdmittedChatAttachments(ctx context.Context, refs []admittedChatAttachment) context.Context {
	return context.WithValue(ctx, admittedChatAttachmentsKey{}, append([]admittedChatAttachment(nil), refs...))
}
func admittedChatAttachmentFor(user, session string, req QueryRequest, hash, path string, bytes int64) admittedChatAttachment {
	return admittedChatAttachment{User: user, Session: session, Project: crewResourceProjectID(req.AgentProfileConversationKey), Conversation: req.AgentProfileConversationKey, Hash: hash, Path: path, Bytes: bytes}
}

// Ready uploads enter the durable queue as references rather than depending on
// the process-local pending-upload waiter surviving a restart. Unfinished ones
// retain the existing waiting/reattach semantics.
func (api *StreamingAPI) bindReadyChatAttachments(ctx context.Context, user, session string, req *QueryRequest) context.Context {
	refs := admittedChatAttachmentsFromContext(ctx)
	var pending []pendingChatAttachment
	for _, attachment := range req.PendingAttachments {
		item, err := api.pendingChatUpload(pendingAttachmentKey(user, session, *req, attachment.Hash))
		if err != nil {
			pending = append(pending, attachment)
			continue
		}
		select {
		case <-item.Ready:
			if item.Path != "" && item.Error == "" {
				refs = append(refs, admittedChatAttachmentFor(user, session, *req, attachment.Hash, item.Path, item.Bytes))
				hasPath := false
				for _, saved := range userMessageAttachments(req.Query) {
					if saved["path"] == item.Path {
						hasPath = true
						break
					}
				}
				if !hasPath {
					req.Query += filesInContextMarker + " " + item.Path
				}
			} else {
				pending = append(pending, attachment)
			}
		default:
			pending = append(pending, attachment)
		}
	}
	req.PendingAttachments = pending
	return withAdmittedChatAttachments(ctx, refs)
}

type preparedChatImageFile struct {
	*os.File
	name string
}

func (f *preparedChatImageFile) Close() error {
	err := f.File.Close()
	_ = os.Remove(f.name)
	return err
}

// Reuse the file tools' current Crew/connection grants and contained roots.
// os.Root and the no-symlink rule prevent a replaced uploaded path escaping.
func (api *StreamingAPI) openAdmittedChatImage(ctx context.Context, ref admittedChatAttachment) (*os.File, error) {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID != ref.User || ref.Session == "" || crewResourceProjectID(ref.Conversation) != ref.Project || userAccessForClaims(claims).Disabled {
		return nil, errors.New("attachment_scope")
	}
	if claims.AccessToken != nil {
		fresh, err := activeExternalGrantClaims(ctx, claims.AccessToken.ID)
		if err != nil || fresh.UserID != ref.User {
			return nil, errors.New("attachment_grant_revoked")
		}
		claims = fresh
		ctx = context.WithValue(ctx, UserContextKey, claims)
	}
	crew, _, _, ok := api.externalCrewResolve(ctx, claims, ref.Project)
	if !ok {
		return nil, errors.New("attachment_crew_unavailable")
	}
	root := crew.Binding.WorkspacePath
	if !strings.HasPrefix(ref.Path, root+"/") {
		return nil, errors.New("attachment_path")
	}
	relative, err := wf.CleanRelative(strings.TrimPrefix(ref.Path, root+"/"))
	guard := externalFileGuard(ctx, true)
	if err != nil || externalPathPrivate(true, relative) || !guard.Allows(relative, false) || (claims.AccessToken != nil && !claims.AccessToken.FileGuard.Allows(relative, false)) {
		return nil, errors.New("attachment_path_denied")
	}
	root, relative, _, guard, _, err = wf.BoundCrewFileRequest(fsutil.WorkspaceDocsRoot(), root, relative, "", guard)
	if err != nil || !guard.Allows(relative, false) {
		return nil, errors.New("attachment_path_denied")
	}
	roots, bound, err := crewStorageRoots(root)
	if err != nil {
		return nil, err
	}
	var directory *os.Root
	if bound {
		physical := roots.ResourcePathRoot(relative)
		if root == roots.FilesPath() {
			physical = roots.Files
		}
		directory, err = os.OpenRoot(physical)
	} else {
		base, e := os.OpenRoot(fsutil.WorkspaceDocsRoot())
		if e != nil {
			return nil, e
		}
		defer base.Close()
		if e = externalNoSymlinks(base, root); e != nil {
			return nil, e
		}
		directory, err = base.OpenRoot(root)
	}
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	if err = externalNoSymlinks(directory, relative); err != nil {
		return nil, err
	}
	file, err := directory.Open(relative)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("attachment_not_file")
	}
	return file, nil
}

// Factories contain only scoped references. Decoding is lazy, after the HTTP
// listener and only when the owned provider actually delivers this message.
func (api *StreamingAPI) withNativeChatImages(ctx context.Context, user, session, provider string, req QueryRequest) context.Context {
	refs := append(admittedChatAttachmentsFromContext(ctx), req.admittedAttachments...)
	ctx = withAdmittedChatAttachments(ctx, refs)
	if len(refs) == 0 {
		return llmtypes.WithNativeImageInputs(ctx, session, nil)
	}
	var inputs []llmtypes.NativeImageInput
	unavailable := !api.nativeChatImagesSupported(ctx, user, session, provider, req.ClaudeCodeTransport)
	fallbackNotice := false
	var sourceBytes int64

	for _, ref := range refs {
		if ref.User != user || ref.Session != session || ref.Conversation != req.AgentProfileConversationKey {
			continue
		}
		if !nativeChatImagePath(ref.Path) {
			switch strings.ToLower(filepath.Ext(ref.Path)) {
			case ".heic", ".heif", ".bmp", ".avif":
				log.Printf("[CHAT_IMAGE] session=%s provider=%s images=1 bytes=%d native=false action=skipped reason=format_unavailable", session, provider, ref.Bytes)
				api.recordChatImageNotice(session, "This image format cannot be sent directly yet. Its file path is still available.")
			}
			continue
		}
		if unavailable {
			log.Printf("[CHAT_IMAGE] session=%s provider=%s images=1 bytes=0 native=false action=skipped reason=program_or_transport_unavailable", session, provider)
			fallbackNotice = true
			continue
		}
		if ref.Bytes > nativeChatImageMaxBytes || sourceBytes+ref.Bytes > nativeChatTurnImageMaxBytes || len(inputs) >= 16 {
			api.recordChatImageNotice(session, "An image was too large to send directly. Its file path is still available.")
			log.Printf("[CHAT_IMAGE] session=%s provider=%s images=1 bytes=%d native=false action=skipped reason=image_limit", session, provider, ref.Bytes)
			continue
		}
		sourceBytes += ref.Bytes
		inputs = append(inputs, llmtypes.NativeImageInput{ID: ref.Hash, Skipped: func(reason string) {
			api.recordChatImageNotice(session, "An image could not be sent directly. Its file path is still available.")
		}, Open: func(deliveryCtx context.Context) (outReader io.ReadCloser, outMIME string, outBytes int64, outErr error) {
			log.Printf("[CHAT_IMAGE] session=%s provider=%s image=%s images=1 bytes=%d native=true action=started reason=preparing", session, provider, ref.Hash, ref.Bytes)
			defer func() {
				if outErr != nil {
					log.Printf("[CHAT_IMAGE] session=%s provider=%s image=%s images=1 bytes=%d native=false action=failed reason=prepare_or_access_refused", session, provider, ref.Hash, ref.Bytes)
				}
			}()
			// The fresh native call's cancellation controls decoding; the admitted
			// principal, not a child tool's inherited identity, authorizes the read.
			auth := context.WithoutCancel(ctx)
			if err := deliveryCtx.Err(); err != nil {
				return nil, "", 0, err
			}

			source, err := api.openAdmittedChatImage(auth, ref)
			if err != nil {
				return nil, "", 0, err
			}
			defer source.Close()
			stat, err := source.Stat()
			if err != nil || ref.Bytes <= 0 || stat.Size() > ref.Bytes {
				return nil, "", 0, errors.New("image_changed_reattach")
			}
			state, err := workflowCLIStateRoot()
			if err != nil {
				return nil, "", 0, err
			}
			dir := filepath.Join(state, "prepared-chat-images")
			if err = os.MkdirAll(dir, 0700); err != nil {
				return nil, "", 0, err
			}
			destination, err := os.CreateTemp(dir, "image-*")
			if err != nil {
				return nil, "", 0, err
			}
			result, err := prepareNativeChatImageContext(deliveryCtx, source, destination)
			if err == nil {
				_, err = destination.Seek(0, io.SeekStart)
			}
			if err != nil {
				destination.Close()
				os.Remove(destination.Name())
				return nil, "", 0, err
			}
			// Unlink while open: killed deliveries leave no image copy on disk. Both
			// Unix desktop and server readers retain the handle until RPC completion.
			if err = os.Remove(destination.Name()); err != nil && runtime.GOOS != "windows" {
				destination.Close()
				return nil, "", 0, err
			}
			if result.FirstFrame {
				api.recordChatImageNotice(session, "GIF attachments are sent as their first frame. The original file is still available.")
			}
			log.Printf("[CHAT_IMAGE] session=%s provider=%s image=%s images=1 bytes=%d native=true action=done reason=prepared first_frame=%t", session, provider, ref.Hash, result.Bytes, result.FirstFrame)
			return &preparedChatImageFile{File: destination, name: destination.Name()}, result.MIME, result.Bytes, nil
		}})
	}
	if fallbackNotice {
		api.recordChatImageNotice(session, "This AI program cannot receive images directly yet. The attached file paths are still available.")
	}
	return llmtypes.WithNativeImageInputs(ctx, session, inputs)
}

func nativeChatImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

func (api *StreamingAPI) recordChatImageNotice(session, message string) {
	if api.eventStore == nil {
		return
	}
	event := storeevents.Event{ID: fmt.Sprintf("chat-image-notice:%d", time.Now().UnixNano()), Type: "chat_image_notice", SessionID: session, Timestamp: time.Now(), ExecutionID: "main:" + session, ParentExecutionID: "session:" + session, ExecutionKind: "main_agent", Data: unifiedevents.NewAgentEvent(&unifiedevents.GenericEventData{Data: map[string]interface{}{"message": message}})}
	api.eventStore.AddEvent(session, event)
}

// The SDK cannot persist attachment capabilities in its string-only steer
// queue. A proven native refusal returns to the existing durable host lane.
func (api *StreamingAPI) queueDeferredChatImage(w http.ResponseWriter, r *http.Request, session string, err error) bool {
	var deliveryErr *mcpagent.CodingAgentDeliveryError
	if !errors.As(err, &deliveryErr) || deliveryErr.Kind != mcpagent.DeliveryErrorKindQueueRequired {
		return false
	}
	input, known := r.Context().Value(retainedQueueInputKey{}).(retainedQueueInput)
	if !known || conversationTurnQueueExecution(r.Context()) || isQueuedMessageSteer(r.Context()) {
		writeAgentProfileError(w, http.StatusConflict, "The image was not delivered. Send it again after this turn finishes.")
		return true
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=%s native=false action=queued reason=native_turn_not_ready", session, deliveryErr.Provider)
	if input.LiveInput {
		return api.queueLiveInputTurnResponse(w, r, GetUserIDFromContext(r.Context()), session, input.Request)
	}
	return api.queueConversationTurnResponse(w, r, GetUserIDFromContext(r.Context()), session, input.Request)
}

// Use the same saved chat choice/server default as the coding runtime. Live
// text-only transports keep paths; no image bytes are ever pasted into tmux.
func (api *StreamingAPI) nativeChatImagesSupported(ctx context.Context, user, session, provider, carried string) bool {
	live := liveCodingProviderTransport(session)
	if provider == "cursor-cli" {
		if live != "" {
			return live == "sdk"
		}
		return strings.ToLower(strings.TrimSpace(os.Getenv("CURSOR_CLI_TRANSPORT"))) != "tmux"
	}
	if provider == "agy-cli" {
		if live != "" {
			return live == "acp"
		}
		// initialize must still explicitly advertise image support before Open.
		return agycli.AgyACPSelected()
	}
	if provider == "muse-cli" {
		if live != "" {
			return live == "msp"
		}
		return musecli.MuseMSPSelected()
	}
	if provider == "pi-cli" {
		// Pi's coding runtime uses native RPC for prompts and steers.
		return true
	}
	if provider == "codex-cli" {
		transport := live
		if transport == "" {
			transport = strings.ToLower(strings.TrimSpace(os.Getenv("CODEX_CLI_TRANSPORT")))
		}
		return transport == "" || transport == "app-server"
	}
	if provider == "claude-code" {
		if live != "" {
			return live == "sdk"
		}
		transport := chatClaudeCodeTransport(ctx, user, session, carried)
		if strings.TrimSpace(transport) == "" {
			transport = os.Getenv("CLAUDE_CODE_TRANSPORT")
		}
		transport = strings.ToLower(strings.TrimSpace(transport))
		return transport == "" || transport == "sdk"
	}
	return false
}
