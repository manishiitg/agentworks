package server

import (
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Notifications (owner, 5 Nov, approved mockup): a person turns them on per device with the bell, and the server
// decides every send: when an agent's turn ends ("<agent> finished · <Crew>", or "couldn't finish" after an error) and
// when an agent needs them, for chats they started or, by their choice, every agent they can see now; never for the
// chat open and focused on that device, never with the chat's content. Browsers get Web Push (VAPID); the Mac app gets
// native notifications through a stream from its own server, which also relays its company servers'. Device records
// and the VAPID keys stay in the server's private state.

const (
	notifyKindWeb = "web"
	notifyKindMac = "mac"

	notifyEventFinished = "finished"
	notifyEventFailed   = "failed"
	notifyEventNeedsYou = "needs_you"

	notifyFocusTTL = 2 * time.Minute
)

type notifyPrefs struct {
	// Chats is "all" for every agent the person can see; "" is chats they started.
	Chats      string `json:"chats,omitempty"`
	NeedsMeOff bool   `json:"needs_me_off,omitempty"`
}

type notifyDevice struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Endpoint string `json:"endpoint,omitempty"`
	P256dh   string `json:"p256dh,omitempty"`
	Auth     string `json:"auth,omitempty"`
	Sound    bool   `json:"sound,omitempty"`
	Added    string `json:"added"`
	LastSent string `json:"last_sent,omitempty"`
}

type notifyRecord struct {
	UserID  string         `json:"user_id"`
	Prefs   notifyPrefs    `json:"prefs"`
	Devices []notifyDevice `json:"devices"`
}

// notifyDeviceView is what the person's own screens see: never the push keys.
type notifyDeviceView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Sound    bool   `json:"sound"`
	Added    string `json:"added"`
	LastSent string `json:"last_sent,omitempty"`
}

var notifyMu sync.Mutex

func notifyDir() (string, error) {
	root, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "notifications"), nil
}

func notifyRecordPath(user string) (string, error) {
	dir, err := notifyDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "people", crewTransferDigest(user)+".json"), nil
}

func readNotifyRecord(user string) (notifyRecord, error) {
	rec := notifyRecord{UserID: user, Devices: []notifyDevice{}}
	path, err := notifyRecordPath(user)
	if err != nil {
		return rec, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return rec, nil
	}
	if err == nil {
		err = json.Unmarshal(raw, &rec)
	}
	if rec.UserID != user {
		return notifyRecord{UserID: user, Devices: []notifyDevice{}}, err
	}
	return rec, err
}

func writeNotifyRecord(rec notifyRecord) error {
	path, err := notifyRecordPath(rec.UserID)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(path, rec, 0o600)
}

// updateNotifyRecord changes one person's record under the lock.
func updateNotifyRecord(user string, change func(*notifyRecord) error) (notifyRecord, error) {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	rec, err := readNotifyRecord(user)
	if err != nil {
		return rec, err
	}
	if err = change(&rec); err != nil {
		return rec, err
	}
	return rec, writeNotifyRecord(rec)
}

// notifyRecordsWithDevices is everyone who has at least one device on.
func notifyRecordsWithDevices() []notifyRecord {
	dir, err := notifyDir()
	if err != nil {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "people", "*.json"))
	out := []notifyRecord{}
	for _, file := range files {
		var rec notifyRecord
		raw, err := os.ReadFile(file)
		if err != nil || json.Unmarshal(raw, &rec) != nil || rec.UserID == "" || len(rec.Devices) == 0 {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// VAPID keys: made once per server, kept in its private state.
type notifyVAPID struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

var (
	notifyVAPIDOnce sync.Once
	notifyVAPIDKeys notifyVAPID
	notifyVAPIDErr  error
)

func notifyVAPIDPair() (notifyVAPID, error) {
	notifyVAPIDOnce.Do(func() {
		dir, err := notifyDir()
		if err != nil {
			notifyVAPIDErr = err
			return
		}
		path := filepath.Join(dir, "vapid.json")
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &notifyVAPIDKeys) == nil && notifyVAPIDKeys.Public != "" && notifyVAPIDKeys.Private != "" {
			return
		}
		private, public, err := webpush.GenerateVAPIDKeys()
		if err == nil {
			err = os.MkdirAll(dir, 0o700)
		}
		if err == nil {
			notifyVAPIDKeys = notifyVAPID{Public: public, Private: private}
			err = fsutil.WriteJSONAtomic(path, notifyVAPIDKeys, 0o600)
		}
		notifyVAPIDErr = err
	})
	return notifyVAPIDKeys, notifyVAPIDErr
}

// notifyPushHost: Web Push endpoints are only the browsers' own push services, so the server never posts to an
// address a caller chose.
func notifyPushHost(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range []string{"fcm.googleapis.com", "android.googleapis.com", "push.services.mozilla.com", "notify.windows.com", "push.apple.com"} {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

// Focus: each app tab says which chat it has open and focused; that device gets nothing for it.
type notifyFocusEntry struct {
	user, session string
	at            time.Time
}

var notifyFocus sync.Map // device id -> notifyFocusEntry

func notifyFocused(device, user, session string) bool {
	value, ok := notifyFocus.Load(device)
	if !ok {
		return false
	}
	entry := value.(notifyFocusEntry)
	return entry.user == user && entry.session == session && time.Since(entry.at) < notifyFocusTTL
}

// Mac app streams: device id -> listeners.
type notifyMessage struct {
	Title      string `json:"title"`
	Body       string `json:"body"`
	Route      string `json:"route"`
	Session    string `json:"session"`
	Event      string `json:"event"`
	Sound      bool   `json:"sound"`
	Connection string `json:"connection,omitempty"` // set by a Mac app's server for a company server's message
}

var notifyStreams = struct {
	sync.Mutex
	byDevice map[string][]chan notifyMessage
}{byDevice: map[string][]chan notifyMessage{}}

func notifyStreamListen(device string) (chan notifyMessage, func()) {
	ch := make(chan notifyMessage, 16)
	notifyStreams.Lock()
	notifyStreams.byDevice[device] = append(notifyStreams.byDevice[device], ch)
	notifyStreams.Unlock()
	return ch, func() {
		notifyStreams.Lock()
		notifyStreams.byDevice[device] = slices.DeleteFunc(notifyStreams.byDevice[device], func(c chan notifyMessage) bool { return c == ch })
		if len(notifyStreams.byDevice[device]) == 0 {
			delete(notifyStreams.byDevice, device)
		}
		notifyStreams.Unlock()
	}
}

func notifyStreamSend(device string, msg notifyMessage) bool {
	notifyStreams.Lock()
	defer notifyStreams.Unlock()
	listeners := notifyStreams.byDevice[device]
	for _, ch := range listeners {
		select {
		case ch <- msg:
		default:
		}
	}
	return len(listeners) > 0
}

// --- Routes ---

func (api *StreamingAPI) registerNotificationRoutes(r *mux.Router) {
	r.HandleFunc("/notifications", api.handleNotifications).Methods("GET", "PUT")
	r.HandleFunc("/notifications/devices", api.handleNotifyDeviceAdd).Methods("POST")
	r.HandleFunc("/notifications/devices/{id}", api.handleNotifyDeviceRemove).Methods("DELETE")
	r.HandleFunc("/notifications/focus", api.handleNotifyFocus).Methods("POST")
	r.HandleFunc("/notifications/stream", api.handleNotifyStream).Methods("GET")
}

// notifyCaller is the signed-in person; machine tokens and MCP callers do not manage a person's devices.
func notifyCaller(w http.ResponseWriter, r *http.Request) (string, bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || claims.AccessToken != nil {
		http.Error(w, `{"error":"Sign in on this device to turn notifications on."}`, http.StatusForbidden)
		return "", false
	}
	return claims.UserID, true
}

func notifyViews(rec notifyRecord) []notifyDeviceView {
	out := []notifyDeviceView{}
	for _, d := range rec.Devices {
		out = append(out, notifyDeviceView{ID: d.ID, Kind: d.Kind, Label: d.Label, Sound: d.Sound, Added: d.Added, LastSent: d.LastSent})
	}
	return out
}

func (api *StreamingAPI) handleNotifications(w http.ResponseWriter, r *http.Request) {
	user, ok := notifyCaller(w, r)
	if !ok {
		return
	}
	rec, err := readNotifyRecord(user)
	if r.Method == http.MethodPut {
		var in struct {
			Chats   string `json:"chats"`
			NeedsMe *bool  `json:"needs_me"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in) != nil || in.Chats != "" && in.Chats != "mine" && in.Chats != "all" {
			http.Error(w, `{"error":"Choose chats you started or every agent you can see."}`, http.StatusBadRequest)
			return
		}
		rec, err = updateNotifyRecord(user, func(rec *notifyRecord) error {
			if in.Chats != "" { // a change names only what it changes
				rec.Prefs.Chats = strings.TrimPrefix(in.Chats, "mine")
			}
			if in.NeedsMe != nil {
				rec.Prefs.NeedsMeOff = !*in.NeedsMe
			}
			return nil
		})
		log.Printf("[NOTIFY] user=%s event=prefs chats=%s needs_me=%t", logUsernameForUserID(user), firstNonEmptyTrimmed(rec.Prefs.Chats, "mine"), !rec.Prefs.NeedsMeOff)
	}
	if err != nil {
		http.Error(w, `{"error":"Notification settings could not be read. Try again."}`, http.StatusInternalServerError)
		return
	}
	vapid, _ := notifyVAPIDPair()
	externalJSON(w, map[string]any{"chats": firstNonEmptyTrimmed(rec.Prefs.Chats, "mine"), "needs_me": !rec.Prefs.NeedsMeOff, "devices": notifyViews(rec), "vapid_public_key": vapid.Public})
}

func (api *StreamingAPI) handleNotifyDeviceAdd(w http.ResponseWriter, r *http.Request) {
	user, ok := notifyCaller(w, r)
	if !ok {
		return
	}
	var in struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Label    string `json:"label"`
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Sound bool `json:"sound"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in) != nil {
		http.Error(w, `{"error":"invalid device"}`, http.StatusBadRequest)
		return
	}
	in.ID, in.Label = strings.TrimSpace(in.ID), strings.TrimSpace(in.Label)
	if _, err := uuid.Parse(in.ID); err != nil || len(in.Label) > 80 {
		http.Error(w, `{"error":"invalid device"}`, http.StatusBadRequest)
		return
	}
	switch in.Kind {
	case notifyKindWeb:
		if !notifyPushHost(in.Endpoint) || in.Keys.P256dh == "" || in.Keys.Auth == "" || len(in.Keys.P256dh) > 200 || len(in.Keys.Auth) > 100 {
			http.Error(w, `{"error":"This browser's notification service isn't supported. Try Chrome, Edge, Firefox or Safari."}`, http.StatusBadRequest)
			return
		}
	case notifyKindMac:
		in.Endpoint, in.Keys.P256dh, in.Keys.Auth = "", "", ""
	default:
		http.Error(w, `{"error":"invalid device"}`, http.StatusBadRequest)
		return
	}
	rec, err := updateNotifyRecord(user, func(rec *notifyRecord) error {
		device := notifyDevice{ID: in.ID, Kind: in.Kind, Label: firstNonEmptyTrimmed(in.Label, "This device"), Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth,
			Sound: in.Sound, Added: time.Now().UTC().Format(time.RFC3339)}
		if at := slices.IndexFunc(rec.Devices, func(d notifyDevice) bool { return d.ID == in.ID }); at >= 0 {
			device.Added, device.LastSent = rec.Devices[at].Added, rec.Devices[at].LastSent
			rec.Devices[at] = device
			return nil
		}
		if len(rec.Devices) >= 20 {
			return fmt.Errorf("too many devices")
		}
		rec.Devices = append(rec.Devices, device)
		return nil
	})
	if err != nil {
		http.Error(w, `{"error":"Notifications could not be turned on. Remove an old device in Settings and try again."}`, http.StatusConflict)
		return
	}
	log.Printf("[NOTIFY] user=%s device=%s event=device_on kind=%s sound=%t", logUsernameForUserID(user), in.ID[:8], in.Kind, in.Sound)
	if in.Kind == notifyKindMac && IsLocalMode() {
		api.kickNotifyRelays()
	}
	externalJSON(w, map[string]any{"devices": notifyViews(rec)})
}

func (api *StreamingAPI) handleNotifyDeviceRemove(w http.ResponseWriter, r *http.Request) {
	user, ok := notifyCaller(w, r)
	if !ok {
		return
	}
	id := mux.Vars(r)["id"]
	rec, err := updateNotifyRecord(user, func(rec *notifyRecord) error {
		rec.Devices = slices.DeleteFunc(rec.Devices, func(d notifyDevice) bool { return d.ID == id })
		return nil
	})
	if err != nil {
		http.Error(w, `{"error":"The device could not be removed. Try again."}`, http.StatusInternalServerError)
		return
	}
	notifyFocus.Delete(id)
	log.Printf("[NOTIFY] user=%s device=%.8s event=device_off", logUsernameForUserID(user), id)
	if IsLocalMode() {
		api.kickNotifyRelays()
	}
	externalJSON(w, map[string]any{"devices": notifyViews(rec)})
}

func (api *StreamingAPI) handleNotifyFocus(w http.ResponseWriter, r *http.Request) {
	user, ok := notifyCaller(w, r)
	if !ok {
		return
	}
	var in struct {
		Device  string `json:"device"`
		Session string `json:"session"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<10)).Decode(&in) != nil || in.Device == "" || len(in.Device) > 64 || len(in.Session) > 200 {
		http.Error(w, `{"error":"invalid focus"}`, http.StatusBadRequest)
		return
	}
	if in.Session == "" {
		notifyFocus.Delete(in.Device)
	} else {
		notifyFocus.Store(in.Device, notifyFocusEntry{user: user, session: in.Session, at: time.Now()})
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleNotifyStream serves a Mac app's device: the person's own messages for that device, as they are sent.
func (api *StreamingAPI) handleNotifyStream(w http.ResponseWriter, r *http.Request) {
	user, ok := notifyCaller(w, r)
	if !ok {
		return
	}
	device := r.URL.Query().Get("device")
	rec, _ := readNotifyRecord(user)
	if !slices.ContainsFunc(rec.Devices, func(d notifyDevice) bool { return d.ID == device && d.Kind == notifyKindMac }) {
		http.Error(w, `{"error":"Turn notifications on for this Mac first."}`, http.StatusNotFound)
		return
	}
	notifyServeStream(w, r, device)
}

func notifyServeStream(w http.ResponseWriter, r *http.Request, device string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, stop := notifyStreamListen(device)
	defer stop()
	_ = writeSSEEvent(w, "ready", -1, struct{}{})
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			if writeSSEEvent(w, "notify", -1, msg) != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleDesktopNotifyStream is the Mac app's own window-independent stream (its main process, not a page): every
// message for this Mac's devices, its company servers' included. Requires this Mac's person's session and a private
// app proof on its loopback host: the startup secret in full launches, the existing owner-only socket proof in split
// development. Neither the session nor a loopback Origin alone is enough.
func (api *StreamingAPI) handleDesktopNotifyStream(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if !IsLocalMode() || claims == nil || claims.AccessToken != nil || claims.UserID != GetDefaultUserID() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if len(desktopSocketKey) != 0 {
		// Split development uses the existing private socket proof and the
		// normal local session. A session or a loopback Origin alone is not proof.
		if !desktopConnectionGate(w, r) {
			return
		}
	} else if desktopStartup.NotifySecret == "" || r.Host != desktopBootstrapHost ||
		!hmac.Equal([]byte(desktopStartup.NotifySecret), []byte(r.Header.Get("X-AgentWorks-Notify"))) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	log.Printf("[NOTIFY] device=%s event=stream_connected", notifyMacAppDevice)
	defer log.Printf("[NOTIFY] device=%s event=stream_disconnected", notifyMacAppDevice)

	notifyServeStream(w, r, notifyMacAppDevice)
}

// notifyMacAppDevice is the in-process address the Mac app's main process listens on; every Mac device record of
// this computer's person is delivered to it.
const notifyMacAppDevice = "mac-app"
