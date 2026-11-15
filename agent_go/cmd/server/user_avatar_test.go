package server

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
	for _, actor := range []*UserClaims{nil, {UserID: "other", Username: "other"}} {
		if r := fetch(actor); r.Code < 400 || bytes.Equal(r.Body.Bytes(), jpegBytes) {
			t.Fatal("private image readable without admission")
		}
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
