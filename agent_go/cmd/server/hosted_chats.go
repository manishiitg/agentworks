package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// Chats inside AgentWorks that a person's own agents can find and message
// (PLAT-861, bullpen-hosted-chats design, owner 2026-10-16): the Builders of
// the person's Crews and Goals, and the person's own Code project chats. The
// server makes the names; nothing is registered. A name is
// <kind>-<slug>-<id4>-<chat>, where id4 comes from the permanent product id so
// two products with one title never collide.

type hostedChat struct {
	Kind      string // crew, goal or code
	ProductID string
	Title     string
	Chat      string // builder, main or a side chat id
	ChatName  string
	Why       string // owner, editor
}

var hostedKindLabel = map[string]string{"crew": "Crew", "goal": "Goal", "code": "Code"}

// hostedSlug is a short, readable piece of a product title, safe inside a name.
func hostedSlug(title string) string {
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 24 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "untitled"
	}
	return slug
}

// hostedID4 is the first four hex characters of the product id's hash.
func hostedID4(productID string) string {
	sum := sha256.Sum256([]byte(productID))
	return hex.EncodeToString(sum[:])[:4]
}

func (h hostedChat) Address() string {
	return h.Kind + "-" + hostedSlug(h.Title) + "-" + hostedID4(h.ProductID) + "-" + hostedSlug(h.Chat)
}

func (h hostedChat) Display() string {
	name := h.ChatName
	if name == "" {
		name = strings.ToUpper(h.Chat[:1]) + h.Chat[1:]
	}
	return hostedKindLabel[h.Kind] + " · " + h.Title + " · " + name
}

func (h hostedChat) item() map[string]interface{} {
	address := h.Address()
	return map[string]interface{}{
		"agent_name": address, "target": "agent:" + address, "display": h.Display(), "kind": "hosted_chat",
		"product": h.Kind, "product_id": h.ProductID, "chat": h.Chat, "reachable_because": h.Why,
		"can_receive": false,
		"note":        "A chat running inside AgentWorks. It is listed so you can see it, but it cannot receive messages from other agents yet; a send to this address is refused.",
	}
}

// hostedChatsFor lists what claims' person may message: Builders of their own
// Crews, Builders of Goals they own or edit, and their own Code project chats.
func (api *StreamingAPI) hostedChatsFor(ctx context.Context, claims *UserClaims) []hostedChat {
	if claims == nil || claims.UserID == "" {
		return nil
	}
	var out []hostedChat
	if crews, err := api.externalCrewsVisible(ctx, claims, ""); err == nil {
		for _, crew := range crews {
			if crew["access"] != "owner" {
				continue
			}
			id, title := stringField(crew["id"]), stringField(crew["name"])
			if id != "" && title != "" {
				out = append(out, hostedChat{Kind: "crew", ProductID: id, Title: title, Chat: "builder", ChatName: "Builder", Why: "owner"})
			}
		}
	}
	if workflows, err := DiscoverWorkflowManifests(ctx); err == nil {
		for _, w := range filterWorkflowManifestsForUser(claims, workflows) {
			if w.Manifest == nil || w.Manifest.Kind == "relay" || (w.MyAccess != WorkflowAccessOwner && w.MyAccess != WorkflowAccessWrite) {
				continue
			}
			why := "owner"
			if w.MyAccess == WorkflowAccessWrite {
				why = "editor"
			}
			title := firstNonEmptyTrimmed(w.Manifest.Label, w.Manifest.ID)
			out = append(out, hostedChat{Kind: "goal", ProductID: w.Manifest.ID, Title: title, Chat: "builder", ChatName: "Builder", Why: why})
		}
	}
	out = append(out, ownCodeChats(ctx, claims.UserID)...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Address() < out[j].Address() })
	return out
}

// ownCodeChats lists the main chat and side chats of the person's own Code projects.
func ownCodeChats(ctx context.Context, userID string) []hostedChat {
	root := agentProfileRuntimeWorkspace(userID, "Chats/Code/projects")
	store := defaultProductProjectStore()
	paths, exists, err := store.listPaths(ctx, root)
	if err != nil || !exists {
		return nil
	}
	prefix := strings.TrimSuffix(filepath.ToSlash(root), "/") + "/"
	var out []hostedChat
	seen := map[string]bool{}
	for _, candidate := range paths {
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if !strings.HasPrefix(candidate, prefix) || !strings.HasSuffix(candidate, "/product.json") || seen[candidate] {
			continue
		}
		seen[candidate] = true
		raw, found, readErr := store.read(ctx, candidate)
		if readErr != nil || !found {
			continue
		}
		var manifest productProjectManifest
		if json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != codeproduct.ProfileID || strings.TrimSpace(manifest.ID) == "" {
			continue
		}
		title := firstNonEmptyTrimmed(manifest.Title, manifest.ID)
		chats, chatErr := defaultProductConversationRegistryStore().projectChats(ctx, userID, codeproduct.ProfileID, manifest.ID)
		if chatErr != nil {
			continue
		}
		for _, record := range chats {
			chat, name := "main", "Main chat"
			if record.ConversationKey != manifest.ID {
				chat = strings.TrimPrefix(record.ConversationKey, manifest.ID+codeChatSideMarker)
				name = "Chat " + chat
			}
			out = append(out, hostedChat{Kind: "code", ProductID: manifest.ID, Title: title, Chat: chat, ChatName: name, Why: "owner"})
		}
	}
	return out
}

func stringField(v interface{}) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
