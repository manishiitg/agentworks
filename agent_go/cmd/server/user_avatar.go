package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const avatarInputBytes = 5 << 20
const avatarStoredBytes = 64 << 10

// Only this small re-encoded JPEG is stored with the account. Public directory
// views use userAvatarFields instead; they never serialize the photo bytes.
type userAvatar struct {
	Icon     string `json:"icon,omitempty"`
	Color    string `json:"color,omitempty"`
	JPEG     string `json:"jpeg,omitempty"`
	Revision string `json:"revision"`
}
type userAvatarFields struct {
	AvatarURL   string `json:"avatar_url,omitempty"`
	AvatarIcon  string `json:"avatar_icon,omitempty"`
	AvatarColor string `json:"avatar_color,omitempty"`
}
type avatarCrop struct {
	Zoom float64 `json:"zoom"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}
type userAvatarRequest struct {
	Operation string      `json:"operation"`
	Icon      string      `json:"icon,omitempty"`
	Color     string      `json:"color,omitempty"`
	Image     []byte      `json:"image_base64,omitempty"`
	Crop      *avatarCrop `json:"crop,omitempty"`
}

func avatarFieldsFor(user, crew string) userAvatarFields {
	dir, err := loadUserDirectory()
	if err != nil || dir == nil {
		return userAvatarFields{}
	}
	rec := dir.byID(user)
	if rec == nil || rec.Disabled || rec.Avatar == nil {
		return userAvatarFields{}
	}
	a := rec.Avatar
	out := userAvatarFields{AvatarIcon: a.Icon, AvatarColor: a.Color}
	if a.JPEG != "" {
		q := url.Values{"revision": {a.Revision}}
		if crew != "" {
			q.Set("crew_id", crew)
		}
		out.AvatarURL = "/api/account/avatars/" + url.PathEscape(user) + "?" + q.Encode()
	}
	return out
}

func normalizeUserAvatar(req userAvatarRequest) (*userAvatar, error) {
	if req.Operation == "remove" {
		return nil, nil
	}
	if req.Color == "" {
		req.Color = "blue"
	}
	switch req.Color {
	case "blue", "purple", "green", "orange":
	default:
		return nil, errors.New("Choose an offered icon colour.")
	}
	a := &userAvatar{Color: req.Color}
	if len(req.Image) == 0 {
		if req.Crop != nil {
			return nil, errors.New("A crop needs a photo.")
		}
		switch req.Icon {
		case "person", "rocket", "star", "leaf", "bot":
			a.Icon = req.Icon
		default:
			return nil, errors.New("Choose a photo or an offered icon.")
		}
	} else {
		if req.Icon != "" {
			return nil, errors.New("Choose a photo or an icon, not both.")
		}
		if len(req.Image) > avatarInputBytes {
			return nil, errors.New("Choose a photo smaller than 5 MB.")
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(req.Image))
		if err != nil || (format != "jpeg" && format != "png" && format != "webp") {
			return nil, errors.New("Choose a PNG, JPEG or WebP photo.")
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
			return nil, errors.New("Choose a photo up to 16 megapixels and 8192 pixels per side.")
		}
		zoom, x, y := 1.0, 0.5, 0.5
		if req.Crop != nil {
			zoom, x, y = req.Crop.Zoom, req.Crop.X, req.Crop.Y
		}
		if math.IsNaN(zoom) || math.IsInf(zoom, 0) || zoom < 1 || zoom > 8 || math.IsNaN(x) || math.IsNaN(y) || x < 0 || x > 1 || y < 0 || y > 1 {
			return nil, errors.New("Choose a crop within the photo, with zoom between 1 and 8.")
		}
		src, _, err := image.Decode(bytes.NewReader(req.Image))
		if err != nil {
			return nil, errors.New("This photo could not be decoded.")
		}
		side := max(1, int(float64(min(cfg.Width, cfg.Height))/zoom))
		left := int(float64(cfg.Width-side) * x)
		top := int(float64(cfg.Height-side) * y)
		dst := image.NewRGBA(image.Rect(0, 0, 256, 256))
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, image.Rect(left, top, left+side, top+side), draw.Over, nil)
		var output bytes.Buffer
		if err = jpeg.Encode(&output, dst, &jpeg.Options{Quality: 85}); err != nil || output.Len() > avatarStoredBytes {
			return nil, errors.New("This photo could not be saved as a small profile picture.")
		}
		a.JPEG = base64.StdEncoding.EncodeToString(output.Bytes())
	}
	digest := sha256.Sum256([]byte(a.Icon + "\x00" + a.Color + "\x00" + a.JPEG))
	a.Revision = hex.EncodeToString(digest[:12])
	return a, nil
}

func (api *StreamingAPI) userAvatar(w http.ResponseWriter, r *http.Request, req userAvatarRequest) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "Sign in to change your profile picture.", 401)
		return
	}
	if userAccessForClaims(claims).Disabled || claims.Scope != "" || (claims.AccessToken != nil && claims.AccessToken.FileGuard != nil) {
		log.Printf("[PROFILE_AVATAR] user=%q action=refused reason=account_or_file_guard", claims.UserID)
		http.Error(w, "Use your own account to change your profile picture.", 403)
		return
	}
	if req.Operation == "" {
		req.Operation = "get"
	}
	switch req.Operation {
	case "get":
		externalJSON(w, avatarFieldsFor(claims.UserID, ""))
		return
	case "set", "remove":
	default:
		http.Error(w, "Choose get, set or remove.", 400)
		return
	}
	log.Printf("[PROFILE_AVATAR] user=%q action=started operation=%s", claims.UserID, req.Operation)
	next, err := normalizeUserAvatar(req)
	if err != nil {
		log.Printf("[PROFILE_AVATAR] user=%q action=refused reason=invalid_image_or_icon", claims.UserID)
		http.Error(w, err.Error(), 400)
		return
	}
	userDirectoryMutationMu.Lock()
	defer userDirectoryMutationMu.Unlock()
	dir, err := readUserDirectoryFile()
	if err != nil {
		log.Printf("[PROFILE_AVATAR] user=%q action=failed reason=account_read_failed", claims.UserID)
		http.Error(w, "Your account could not be loaded.", 503)
		return
	}
	rec := dir.byID(claims.UserID)
	if rec == nil {
		// Local single-person installs may not yet have a directory entry. Their
		// established machine-owner authority is preserved, never created on a server.
		if IsMultiUserMode() {
			log.Printf("[PROFILE_AVATAR] user=%q action=refused reason=unregistered_account", claims.UserID)
			http.Error(w, "Sign in to a registered account to change your picture.", 409)
			return
		}
		dir.Users = append(dir.Users, UserRecord{ID: claims.UserID, Username: claims.Username, Email: claims.Email, Admin: true, CanCreate: true, Role: UserRoleAdmin, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
		rec = &dir.Users[len(dir.Users)-1]
	}
	rec.Avatar = next
	if err = saveUserDirectory(dir); err != nil {
		log.Printf("[PROFILE_AVATAR] user=%q action=failed reason=account_write_failed", claims.UserID)
		http.Error(w, "Your picture could not be saved.", 503)
		return
	}
	log.Printf("[PROFILE_AVATAR] user=%q action=done operation=%s", claims.UserID, req.Operation)
	externalJSON(w, avatarFieldsFor(claims.UserID, ""))
}

func (api *StreamingAPI) handleUserAvatar(w http.ResponseWriter, r *http.Request) {
	req := userAvatarRequest{Operation: "get"}
	if r.Method == http.MethodPut {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 7<<20))
		dec.DisallowUnknownFields()
		if dec.Decode(&req) != nil {
			http.Error(w, "Send only the offered profile picture fields.", 400)
			return
		}
	}
	api.userAvatar(w, r, req)
}

// An image URL grants no access: each fetch rechecks current own/directory/Team
// admission. Scoped Team callers lose access on removal; ordinary signed-in
// company accounts retain the existing enabled-people directory audience.
func (api *StreamingAPI) canReadUserAvatar(r *http.Request, subject, crew string) bool {
	c := GetUserFromContext(r.Context())
	if c == nil || c.UserID == "" || userAccessForClaims(c).Disabled || c.Scope != "" || (c.AccessToken != nil && c.AccessToken.FileGuard != nil) {
		return false
	}
	if c.UserID == subject {
		return true
	}
	// Browser accounts already see enabled company people through /users/directory.
	if c.AccessToken == nil {
		return true
	}
	if crew == "" || !crewTeamClaims(c, crew, false) {
		return false
	}
	t, err := readCrewTeam(crew)
	if err != nil {
		return false
	}
	if t.invited(c.UserID) {
		return subject == t.OwnerID
	}
	if !t.member(c.UserID) {
		return false
	}
	if crewTeamCanManage(t, c.UserID) && crewTeamClaims(c, crew, true) {
		return true
	} // same people-directory admission
	v := api.crewPeopleViewFor(c, t)
	if v.Owner.UserID == subject {
		return true
	}
	for _, group := range [][]crewPeoplePerson{v.People, v.DashboardPeople} {
		for _, p := range group {
			if p.UserID == subject {
				return true
			}
		}
	}
	return false
}
func (api *StreamingAPI) handleUserAvatarImage(w http.ResponseWriter, r *http.Request) {
	user := mux.Vars(r)["user"]
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if user == "" || strings.ContainsAny(user, "/\\\x00\r\n\t") || !api.canReadUserAvatar(r, user, r.URL.Query().Get("crew_id")) {
		http.Error(w, "Profile picture is not available.", 404)
		return
	}
	dir, err := loadUserDirectory()
	if err != nil || dir == nil {
		http.Error(w, "Profile picture is not available.", 404)
		return
	}
	rec := dir.byID(user)
	if rec == nil || rec.Disabled || rec.Avatar == nil || rec.Avatar.JPEG == "" || r.URL.Query().Get("revision") != rec.Avatar.Revision {
		http.Error(w, "Profile picture is not available.", 404)
		return
	}
	data, err := base64.StdEncoding.DecodeString(rec.Avatar.JPEG)
	if err != nil || len(data) > avatarStoredBytes {
		http.Error(w, "Profile picture is not available.", 404)
		return
	}
	log.Printf("[PROFILE_AVATAR] user=%q action=served", user)
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data)
}

// Sender identity comes from the admitted endpoint, never a display-name match.
// A remote AgentBridge user ID is not a local company account.
func agentMessageAvatarFor(source agentMessageEndpoint) userAvatarFields {
	if source.ConditionJob != "" {
		return userAvatarFields{AvatarIcon: "eye"}
	}
	if source.External {
		return userAvatarFields{}
	}
	crew := ""
	if source.Kind == triggerCallerCrew {
		crew = source.ID
	}
	return avatarFieldsFor(source.UserID, crew)
}
