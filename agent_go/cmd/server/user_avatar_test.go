package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

// PLAT-746-1041: actual account handlers may change only the caller's avatar;
// image responses are authenticated, bounded re-encodings, never source files.
func TestUserAvatarOwnWritesAndAuthenticatedJPEG(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	saved := withMemoryUserDirectory(t, `{"users":[{"id":"me","username":"example","role":"creator","products":["work"]},{"id":"other","username":"other","role":"creator","products":["work"]}]}`)
	api := &StreamingAPI{}
	me := &UserClaims{UserID: "me", Username: "example"}
	request := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		api.handleUserAvatar(w, adminRequest(http.MethodPut, "/api/account/avatar", body, me, nil))
		return w
	}
	w := request(`{"operation":"set","icon":"rocket","color":"purple"}`)
	if w.Code != 200 {
		t.Fatalf("set icon: %d %s", w.Code, w.Body.String())
	}
	original := *saved
	scoped := httptest.NewRecorder()
	api.handleUserAvatar(scoped, adminRequest(http.MethodPut, "/api/account/avatar", `{"operation":"set","icon":"star"}`, &UserClaims{UserID: "me", Scope: "report-preview"}, nil))
	if scoped.Code != 403 || *saved != original {
		t.Fatal("scoped report token changed account")
	}

	if w = request(`{"operation":"set","icon":"leaf","user_id":"other"}`); w.Code != 400 || *saved != original {
		t.Fatal("a target selector changed another account")
	}
	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			src.Set(x, y, color.RGBA{R: 240, G: 80, B: 40, A: 255})
		}
	}
	var photo bytes.Buffer
	if err := png.Encode(&photo, src); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(userAvatarRequest{Operation: "set", Image: photo.Bytes(), Crop: &avatarCrop{Zoom: 2, X: .5, Y: .5}})
	w = request(string(raw))
	if w.Code != 200 {
		t.Fatalf("set photo: %d %s", w.Code, w.Body.String())
	}
	var fields userAvatarFields
	if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil || fields.AvatarURL == "" {
		t.Fatal("missing authenticated photo URL")
	}
	var doc userDirectoryFile
	if err := json.Unmarshal([]byte(*saved), &doc); err != nil {
		t.Fatal(err)
	}
	dir := userDirectory{Users: doc.Users}
	mine := dir.byID("me")
	other := dir.byID("other")
	if mine.Role != "creator" || mine.Admin || len(mine.Products) != 1 || other.Avatar != nil || mine.Avatar == nil {
		t.Fatal("avatar write changed unrelated account authority")
	}
	jpegBytes, err := base64.StdEncoding.DecodeString(mine.Avatar.JPEG)
	if err != nil || len(jpegBytes) > avatarStoredBytes {
		t.Fatal("unbounded stored photo")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpegBytes))
	if err != nil || cfg.Width != 256 || cfg.Height != 256 {
		t.Fatal("photo was not square re-encoded")
	}
	fetch := func(actor *UserClaims) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		api.handleUserAvatarImage(w, adminRequest(http.MethodGet, fields.AvatarURL, "", actor, map[string]string{"user": "me"}))
		return w
	}
	for _, actor := range []*UserClaims{nil, {UserID: "me", Scope: "report-preview"}, {UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"files:read"}}}} {
		if r := fetch(actor); r.Code < 400 || bytes.Equal(r.Body.Bytes(), jpegBytes) {
			t.Fatal("private image readable without admission")
		}
	}
	if allowed := fetch(&UserClaims{UserID: "other", Username: "other"}); allowed.Code != 200 {
		t.Fatal("enabled company directory peer could not read photo")
	}
	got := fetch(me)
	if got.Code != 200 || got.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(got.Body.Bytes(), jpegBytes) {
		t.Fatal("own authenticated image unavailable")
	}
	if bytes.Contains(w.Body.Bytes(), []byte(mine.Avatar.JPEG)) {
		t.Fatal("metadata included photo bytes")
	}
	w = request(`{"operation":"remove"}`)
	if w.Code != 200 {
		t.Fatal("remove failed")
	}
	if got = fetch(me); got.Code != 404 {
		t.Fatal("removed photo URL remained readable")
	}
}

func TestUserAvatarRejectsUnsafeImageAndCrop(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	huge := bytes.Clone(b.Bytes())
	binary.BigEndian.PutUint32(huge[16:20], 9000)
	binary.BigEndian.PutUint32(huge[29:33], crc32.ChecksumIEEE(huge[12:29]))
	cases := []userAvatarRequest{
		{Operation: "set", Image: []byte(`<svg onload="execute()"/>`)},
		{Operation: "set", Image: bytes.Repeat([]byte("x"), avatarInputBytes+1)},
		{Operation: "set", Image: huge},
		{Operation: "set", Image: b.Bytes(), Crop: &avatarCrop{Zoom: 1, X: -1, Y: .5}},
		{Operation: "set", Image: b.Bytes(), Crop: &avatarCrop{Zoom: 0, X: .5, Y: .5}},
		{Operation: "set", Icon: "javascript:alert(1)"},
		{Operation: "set", Icon: "rocket", Color: "url(example.com)"},
	}
	for i, c := range cases {
		if _, err := normalizeUserAvatar(c); err == nil {
			t.Fatalf("unsafe case %d accepted", i)
		}
	}
}

func TestUserAvatarSettingsToolUsesOwnAccountAndAdmittedImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	panels := &recordingRegistrar{}
	if err := f.api.registerWorkspacePanelChatTools(panels, nil, "owner", "profile-chat", externalCommandCrewRoot, "work"); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	if _, err := panels.tools["profile_picture"].exec(ctx, map[string]any{"operation": "set", "icon": "leaf"}); err != nil {
		t.Fatal(err)
	}
	otherCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "other"})
	if _, err := panels.tools["profile_picture"].exec(otherCtx, map[string]any{"operation": "set", "icon": "star"}); err == nil {
		t.Fatal("chat profile tool changed the bound maker as another person")
	}
	f.api.lastQueryRequests = map[string]QueryRequest{"profile-chat": {AgentMessageFromOtherPerson: true}}
	if _, err := panels.tools["profile_picture"].exec(ctx, map[string]any{"operation": "set", "icon": "star"}); err == nil {
		t.Fatal("teammate-triggered turn changed maker picture")
	}
	icon := f.call(t, "owner", "settings", map[string]any{"action": "profile", "operation": "set", "icon": "rocket", "color": "green"})
	if icon.Code != 200 {
		t.Fatalf("own settings icon: %d %s", icon.Code, icon.Body.String())
	}
	if avatarFieldsFor("owner", "").AvatarIcon != "rocket" || avatarFieldsFor("other", "").AvatarIcon != "" {
		t.Fatal("profile tool crossed accounts")
	}
	refused := f.call(t, "owner", "settings", map[string]any{"action": "profile", "operation": "set", "icon": "leaf", "user_id": "other"})
	if refused.Code < 400 {
		t.Fatal("profile settings accepted target account")
	}
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	// Use the exact relative Crew file admission used for chat attachments.
	f.write(t, externalCommandCrewRoot+"/photo.png", photo.String())
	upload := f.call(t, "owner", "settings", map[string]any{"action": "profile", "operation": "set", "image_file": map[string]any{"crew_id": "team", "path": "photo.png"}})
	if upload.Code != 200 || avatarFieldsFor("owner", "").AvatarURL == "" {
		t.Fatalf("own admitted image: %d %s", upload.Code, upload.Body.String())
	}
	for _, path := range []string{"../../config/users.json", "/etc/passwd", "https://example.com/photo.png"} {
		denied := f.call(t, "owner", "settings", map[string]any{"action": "profile", "operation": "set", "image_file": map[string]any{"crew_id": "team", "path": path}})
		if denied.Code < 400 {
			t.Fatal("profile image escaped admitted Crew file")
		}
	}
	if got := agentMessageAvatarFor(agentMessageEndpoint{UserID: "owner", External: true}); got.AvatarURL != "" || got.AvatarIcon != "" {
		t.Fatal("external account ID resolved to local portrait")
	}
}
