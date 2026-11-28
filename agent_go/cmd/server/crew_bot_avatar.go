package server

import (
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
)

// A bot's picture (#4545) reuses the people avatar: a small photo, or an icon on a colour; the emoji stays the third
// choice (crewBot.Icon). The photo is a re-encoded JPEG of at most 64 KB kept in the server's state area by its
// revision, so the project registry stays small; the registry keeps only the icon, colour and photo revision.
type crewBotAvatar struct {
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color,omitempty"`
	Photo string `json:"photo,omitempty"` // the photo's revision
}

// avatarBotIcons are the icons a bot may pick: the people icons plus a few that say what a bot does.
var avatarBotIcons = append(append([]string{}, avatarPersonIcons...), "book", "bug", "wrench", "chart", "search", "message")

var crewBotPhotoRevision = regexp.MustCompile(`^[0-9a-f]{24}$`)

func crewBotPhotoPath(revision string) (string, bool) {
	if !crewBotPhotoRevision.MatchString(revision) {
		return "", false
	}
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", false
	}
	return filepath.Join(root, "bot-avatars", revision+".jpg"), true
}

// crewBotAvatarFrom checks a maker's picture change and keeps a new photo on disk. A nil result with no error removes
// the picture (the bot shows its emoji).
func crewBotAvatarFrom(req userAvatarRequest) (*crewBotAvatar, error) {
	a, err := normalizeAvatar(req, avatarBotIcons)
	if err != nil || a == nil {
		return nil, err
	}
	out := &crewBotAvatar{Icon: a.Icon, Color: a.Color}
	if a.JPEG != "" {
		data, err := base64.StdEncoding.DecodeString(a.JPEG)
		path, ok := crewBotPhotoPath(a.Revision)
		if err != nil || !ok {
			return nil, errBotPhotoUnsaved
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, errBotPhotoUnsaved
		}
		if err := os.WriteFile(path, data, 0o640); err != nil {
			return nil, errBotPhotoUnsaved
		}
		out.Icon, out.Photo = "", a.Revision
	}
	return out, nil
}

var errBotPhotoUnsaved = errors.New("The picture could not be saved. Try again.")

// crewBotAvatarFields is the bot's picture as views carry it: the same fields as a person's avatar. The photo URL is
// relative to the bot's own server; it grants nothing, since every fetch rechecks that the bot admits the caller.
func crewBotAvatarFields(id string, bot crewBot) userAvatarFields {
	if bot.Avatar == nil {
		return userAvatarFields{}
	}
	out := userAvatarFields{AvatarIcon: bot.Avatar.Icon, AvatarColor: bot.Avatar.Color}
	if bot.Avatar.Photo != "" {
		out.AvatarURL = "/api/bots/avatar?" + url.Values{"bot": {id}, "revision": {bot.Avatar.Photo}}.Encode()
	}
	return out
}

// serveCrewBotAvatar sends a bot's photo to someone the bot admits (its makers included).
func serveCrewBotAvatar(w http.ResponseWriter, r *http.Request, caller string) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	id, revision := r.URL.Query().Get("bot"), r.URL.Query().Get("revision")
	rec, ok := crewBotRecordByID(id)
	if !ok || !crewBotAdmits(rec, id, caller) {
		http.Error(w, "Picture is not available.", http.StatusNotFound)
		return
	}
	bot := rec.Bots[id]
	path, valid := crewBotPhotoPath(revision)
	if bot.Avatar == nil || bot.Avatar.Photo != revision || !valid {
		http.Error(w, "Picture is not available.", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > avatarStoredBytes {
		log.Printf("[CREW_BOT] bot=%s action=avatar_missing", id)
		http.Error(w, "Picture is not available.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data)
}
