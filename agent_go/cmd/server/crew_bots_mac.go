package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

// Bots in a project on a Mac (owner, 28 Nov: "yes it should add it"). Bots live on the server; this Mac reaches the
// server it is signed in to through the saved sign-in, as the person, so the server still decides who may use which
// bot on every call. A Mac project keeps the bots added to it in its own registry record (AddedBots, with BotsServer the
// sign-in they come from); its agents ask them with the same bots tool, which this Mac passes on to that server.

// macBotsConnection is the saved sign-in bots come from: the project's own when it has one, else the first.
func macBotsConnection(owner, preferred string) (desktopConnection, error) {
	saved, err := readDesktopConnections(owner)
	if err != nil || len(saved) == 0 {
		return desktopConnection{}, fmt.Errorf("Sign in to your company's server in the AgentWorks app first; bots come from there.")
	}
	for _, c := range saved {
		if preferred != "" && c.ID == preferred {
			return c, nil
		}
	}
	if preferred != "" {
		return desktopConnection{}, fmt.Errorf("This project's bots come from a server this Mac is no longer signed in to. Sign in to it again in the AgentWorks app.")
	}
	return saved[0], nil
}

// macServerJSON is one request to the server as the person, through the saved sign-in.
func macServerJSON(ctx context.Context, owner string, c desktopConnection, method, route string, input, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// A refusal comes back as the server's own plain message.
	return crewTeamRemoteJSON(ctx, owner, c, method, route, input, out)
}

// macServerBots are the bots the server shares with the person.
func macServerBots(ctx context.Context, owner string, c desktopConnection) ([]crewBotView, error) {
	var out struct {
		Bots []crewBotView `json:"bots"`
	}
	if err := macServerJSON(ctx, owner, c, http.MethodGet, "/api/bots", nil, &out); err != nil {
		return nil, err
	}
	return out.Bots, nil
}

// macProjectRecord is the registry record of a project on this Mac.
func macProjectRecord(projectID string) (string, projectOwnerRecord, error) {
	root := crewProjectRootByID(strings.TrimSpace(projectID))
	rec, ok := crewOwnerRecordAt(root)
	if root == "" || !ok {
		return "", projectOwnerRecord{}, fmt.Errorf("Project not found.")
	}
	return root, rec, nil
}

// macProjectBots are the bots added to a project on this Mac, as the server shares them with the person now;
// unavailable are those it no longer shares.
func macProjectBots(ctx context.Context, owner, projectID string) ([]crewBotView, []string, error) {
	_, rec, err := macProjectRecord(projectID)
	if err != nil {
		return nil, nil, err
	}
	if len(rec.AddedBots) == 0 {
		return []crewBotView{}, []string{}, nil
	}
	c, err := macBotsConnection(owner, rec.BotsServer)
	if err != nil {
		return nil, nil, err
	}
	shared, err := macServerBots(ctx, owner, c)
	if err != nil {
		return nil, nil, err
	}
	out, unavailable := []crewBotView{}, []string{}
	for _, id := range rec.AddedBots {
		i := slices.IndexFunc(shared, func(bot crewBotView) bool { return bot.ID == id })
		if i < 0 {
			unavailable = append(unavailable, id)
			continue
		}
		view := shared[i]
		view.InProjects = nil
		out = append(out, view)
	}
	return out, unavailable, nil
}

// setMacProjectBot adds a bot the server shares with the person to a project on this Mac, or takes it out.
func setMacProjectBot(ctx context.Context, owner, projectID, bot string, add bool) error {
	root, rec, err := macProjectRecord(projectID)
	if err != nil {
		return err
	}
	server := rec.BotsServer
	if add {
		c, err := macBotsConnection(owner, server)
		if err != nil {
			return err
		}
		shared, err := macServerBots(ctx, owner, c)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(shared, func(view crewBotView) bool { return view.ID == bot }) {
			return fmt.Errorf("This bot is not shared with you.")
		}
		server = c.ID
	}
	identity, ok := projectIdentityOf(root)
	if !ok {
		return fmt.Errorf("Project unavailable")
	}
	err = defaultProjectOwners().modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		id := projectOwnerKey(identity.Product, identity.Folder)
		record, exists := projects[id]
		if !exists {
			return false, fmt.Errorf("Project unavailable")
		}
		if containsID(record.AddedBots, bot) == add {
			return false, nil
		}
		next := slices.DeleteFunc(slices.Clone(record.AddedBots), func(b string) bool { return b == bot })
		if add {
			if len(next) >= 50 {
				return false, fmt.Errorf("A project can have at most 50 bots.")
			}
			next = append(next, bot)
		}
		if len(next) == 0 {
			next, server = nil, ""
		}
		record.AddedBots, record.BotsServer = next, server
		projects[id] = record
		return true, nil
	})
	if err == nil {
		log.Printf("[CREW_BOT] mac project=%s bot=%s action=%s", projectID, bot, map[bool]string{true: "add_to_project", false: "remove_from_project"}[add])
	}
	return err
}

// macBotProject is a project on this Mac a bot can be added to, with the bots already in it.
type macBotProject struct {
	ProjectID string   `json:"project_id"`
	Title     string   `json:"title"`
	AddedBots []string `json:"added_bots"`
}

func macBotProjects() []macBotProject {
	records, err := defaultProjectOwners().All()
	if err != nil {
		return []macBotProject{}
	}
	out := []macBotProject{}
	for _, rec := range records {
		if rec.Product != crewProfileID || rec.ProjectID == "" || crewRelayTwin(rec.ProjectID) {
			continue
		}
		_, title := crewOwnerAndTitle(rec.ProjectID)
		added := rec.AddedBots
		if added == nil {
			added = []string{}
		}
		out = append(out, macBotProject{ProjectID: rec.ProjectID, Title: firstNonEmptyTrimmed(title, rec.Folder), AddedBots: added})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out
}

// handleMacProjectBots serves the bots routes on a Mac: the projects here (to add a bot to) and each one's bots.
// GET  /api/bots/mac-projects                  this Mac's projects, with the bots added to each
// GET  /api/bots/project?project_id=           the bots added to a project here
// POST /api/bots/project                       add a server bot to a project here, or take it out
func (api *StreamingAPI) handleMacProjectBots(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		externalError(w, 401, "authentication_required", "Sign in to use bots.")
		return
	}
	owner := claims.UserID
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/bots/mac-projects"):
		externalJSON(w, map[string]any{"projects": macBotProjects()})
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/bots/project"):
		bots, unavailable, err := macProjectBots(r.Context(), owner, r.URL.Query().Get("project_id"))
		if err != nil {
			externalError(w, 409, "bots_unavailable", err.Error())
			return
		}
		externalJSON(w, map[string]any{"bots": bots, "unavailable": unavailable})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/bots/project"):
		var body struct {
			ProjectID string `json:"project_id"`
			Bot       string `json:"bot"`
			Add       bool   `json:"add"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			externalError(w, 400, "invalid_arguments", "invalid request")
			return
		}
		if err := setMacProjectBot(r.Context(), owner, body.ProjectID, strings.TrimSpace(body.Bot), body.Add); err != nil {
			externalError(w, 400, "bot_refused", err.Error())
			return
		}
		bots, unavailable, _ := macProjectBots(r.Context(), owner, body.ProjectID)
		externalJSON(w, map[string]any{"bots": bots, "unavailable": unavailable})
	default:
		externalError(w, 404, "server_only", "Bots are made and used on the server; a project on this Mac can add them.")
	}
}

// registerMacProjectBotsTool gives a Mac project's chat the bots tool: add and remove change this project; list, ask,
// read and call go to the server the Mac is signed in to, as the person; making a bot (get, set) happens in a project
// on the server.
func (api *StreamingAPI) registerMacProjectBotsTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, projectID string) error {
	var description string
	var props map[string]any
	externalBotsDefinition(func(_ string, d string, _ bool, _ bool, p map[string]any, _ ...string) {
		description, props = d, p
	})
	owner := claims.UserID
	gate.Declare(externalBotsTool)
	return reg.RegisterCustomToolWithTimeout(externalBotsTool, description+" This project is on your Mac: its bots come from the server this Mac is signed in to; add and remove default to this project.", map[string]any{"type": "object", "properties": props, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		str := func(key string) string { return strings.TrimSpace(externalArg(args, key)) }
		project := str("crew_id")
		if project == "" {
			project = projectID
		}
		reply := func(value any) (string, error) {
			data, err := json.Marshal(value)
			return string(data), err
		}
		_, rec, err := macProjectRecord(projectID)
		if err != nil {
			return "", err
		}
		c, connErr := macBotsConnection(owner, rec.BotsServer)
		switch str("action") {
		case "add", "remove":
			if err := setMacProjectBot(ctx, owner, project, str("bot"), str("action") == "add"); err != nil {
				return "", err
			}
			bots, unavailable, err := macProjectBots(ctx, owner, project)
			if err != nil {
				return "", err
			}
			return reply(map[string]any{"project_bots": bots, "unavailable": unavailable})
		case "list":
			if connErr != nil {
				return "", connErr
			}
			shared, err := macServerBots(ctx, owner, c)
			if err != nil {
				return "", err
			}
			bots, unavailable, _ := macProjectBots(ctx, owner, project)
			return reply(map[string]any{"bots": shared, "project_bots": bots, "project_bots_unavailable": unavailable})
		case "ask":
			if connErr != nil {
				return "", connErr
			}
			var out map[string]any
			err := macServerJSON(ctx, owner, c, http.MethodPost, "/api/bots/ask", map[string]string{"bot": str("bot"), "message": str("message"), "inbox_id": str("inbox_id"), "submission_id": str("submission_id")}, &out)
			if err != nil {
				return "", err
			}
			return reply(out)
		case "read":
			if connErr != nil {
				return "", connErr
			}
			var out map[string]any
			if err := macServerJSON(ctx, owner, c, http.MethodGet, "/api/bots/read?inbox_id="+url.QueryEscape(str("inbox_id")), nil, &out); err != nil {
				return "", err
			}
			return reply(out)
		case "call":
			if connErr != nil {
				return "", connErr
			}
			input, _ := args["input"].(map[string]any)
			var out map[string]any
			if err := macServerJSON(ctx, owner, c, http.MethodPost, "/api/bots/call", map[string]any{"bot": str("bot"), "function": str("function"), "args": input, "submission_id": str("submission_id")}, &out); err != nil {
				return "", err
			}
			return reply(out)
		case "get", "set":
			return "", fmt.Errorf("Bots are made from agents of projects on the server. This project is on your Mac: it can add and use bots, not make them.")
		default:
			return "", fmt.Errorf("action must be list, ask, read, call, add or remove.")
		}
	}, time.Minute, "workspace_advanced")
}
