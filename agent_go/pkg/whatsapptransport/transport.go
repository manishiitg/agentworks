// Package whatsapptransport is the one WhatsApp transport for every
// AgentWorks product: the whatsmeow session store, one Account per linked
// phone (connect, pair by QR, send text and documents, react, download
// media), incoming-text extraction, and message de-duplication.
//
// It deliberately knows nothing about routing or policy — which chat is
// allowed, what an attachment means, who gets a reply. Those stay with the
// product. See docs/design/reusable_vertical_product_platform.md for the
// shared transport boundary.
package whatsapptransport

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite" // pure-Go sqlite driver, registered as "sqlite"
)

// Store is the whatsmeow device store: one sqlite file holding every linked
// device's session keys.
type Store struct {
	container    *sqlstore.Container
	clientLogger waLog.Logger
}

// OpenStore opens (creating if needed) the session store at dbPath. appName
// and appVersion are what the phone shows under Linked Devices. With debug,
// whatsmeow's own protocol logging goes to stdout.
func OpenStore(ctx context.Context, dbPath, appName string, appVersion [3]uint32, debug bool) (*Store, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, fmt.Errorf("whatsapp: session DB path not configured")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("whatsapp: mkdir session dir: %w", err)
	}
	store.SetOSInfo(appName, appVersion)
	dbLogger, clientLogger := waLog.Noop, waLog.Noop
	if debug {
		dbLogger = waLog.Stdout("WhatsApp-DB", "DEBUG", true)
		clientLogger = waLog.Stdout("WhatsApp", "DEBUG", true)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath)
	container, err := sqlstore.New(ctx, "sqlite", dsn, dbLogger)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: open session store: %w", err)
	}
	return &Store{container: container, clientLogger: clientLogger}, nil
}

// Devices lists every linked device (each becomes an Account).
func (s *Store) Devices(ctx context.Context) ([]*store.Device, error) {
	return s.container.GetAllDevices(ctx)
}

// FirstDevice returns the first linked device, or a fresh unpaired one —
// the single-account shape.
func (s *Store) FirstDevice(ctx context.Context) (*store.Device, error) {
	return s.container.GetFirstDevice(ctx)
}

// NewDevice is an unpaired device for a pairing attempt.
func (s *Store) NewDevice() *store.Device { return s.container.NewDevice() }

// Close releases the underlying session database.
func (s *Store) Close() error {
	if s == nil || s.container == nil {
		return nil
	}
	return s.container.Close()
}

// DeleteDevice forgets a device's session (after Logout).
func (s *Store) DeleteDevice(ctx context.Context, device *store.Device) error {
	return s.container.DeleteDevice(ctx, device)
}

// NewAccount wraps a device in a client and registers the event handler.
// The handler receives raw whatsmeow events; DescribeEvent turns the
// lifecycle ones into log lines.
func (s *Store) NewAccount(device *store.Device, handler func(rawEvt interface{})) *Account {
	client := whatsmeow.NewClient(device, s.clientLogger)
	if handler != nil {
		client.AddEventHandler(handler)
	}
	return &Account{client: client}
}

// Account is one linked phone. Immutable after construction; safe for
// concurrent use because whatsmeow's client is.
type Account struct {
	client *whatsmeow.Client
}

// Device is the underlying store entry (for DeleteDevice).
func (a *Account) Device() *store.Device {
	if a == nil || a.client == nil {
		return nil
	}
	return a.client.Store
}

// OwnJID is the phone-number identity, empty until paired.
func (a *Account) OwnJID() types.JID {
	if a == nil || a.client == nil || a.client.Store == nil || a.client.Store.ID == nil {
		return types.JID{}
	}
	return *a.client.Store.ID
}

// OwnLID is the "@lid" identity modern WhatsApp uses alongside the phone
// number; self-chat messages arrive on it.
func (a *Account) OwnLID() types.JID {
	if a == nil || a.client == nil || a.client.Store == nil {
		return types.JID{}
	}
	return a.client.Store.LID
}

// IsSelfChat is true only for this account's own "Message Yourself" chat,
// matched on both the phone-number JID and the LID.
func (a *Account) IsSelfChat(chat types.JID) bool {
	if chat.User == "" {
		return false
	}
	if own := a.OwnJID(); !own.IsEmpty() && chat.Server == types.DefaultUserServer && chat.User == own.User {
		return true
	}
	if lid := a.OwnLID(); !lid.IsEmpty() && chat.Server == types.HiddenUserServer && chat.User == lid.User {
		return true
	}
	return false
}

// SelfChat is the addressable JID of this account's own chat: the own JID
// without its device part, which SendMessage rejects as a recipient.
func (a *Account) SelfChat() (types.JID, error) {
	own := a.OwnJID()
	if own.IsEmpty() {
		return types.JID{}, fmt.Errorf("whatsapp own JID unknown")
	}
	return own.ToNonAD(), nil
}

func (a *Account) Connect() error {
	if a == nil || a.client == nil {
		return fmt.Errorf("whatsapp: no client")
	}
	return a.client.Connect()
}

func (a *Account) Disconnect() {
	if a != nil && a.client != nil {
		a.client.Disconnect()
	}
}

func (a *Account) IsConnected() bool {
	return a != nil && a.client != nil && a.client.IsConnected()
}

func (a *Account) Logout(ctx context.Context) error {
	if a == nil || a.client == nil {
		return nil
	}
	return a.client.Logout(ctx)
}

// React adds (or with emoji "" clears) a reaction on an incoming message —
// the "got it / working on it" acknowledgement while a turn runs.
func (a *Account) React(ctx context.Context, chat, sender types.JID, msgID types.MessageID, emoji string) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("whatsapp: no client")
	}
	_, err := a.client.SendMessage(ctx, chat, a.client.BuildReaction(chat, sender, msgID, emoji))
	return err
}

// SendText sends one plain-text message.
func (a *Account) SendText(ctx context.Context, chat types.JID, text string) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("whatsapp: no client")
	}
	_, err := a.client.SendMessage(ctx, chat, &waProto.Message{Conversation: &text})
	return err
}

// SendTextWithRetry retries transient send failures (a Signal identity-check
// timeout right after a reconnect, seen live) with a growing pause.
func (a *Account) SendTextWithRetry(chat types.JID, text string, attempts int, attemptTimeout time.Duration) error {
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), attemptTimeout)
		err = a.SendText(ctx, chat, text)
		cancel()
		if err == nil {
			return nil
		}
		log.Printf("[whatsapp] send attempt %d/%d failed: %v", attempt, attempts, err)
		if attempt < attempts {
			time.Sleep(time.Duration(attempt) * 3 * time.Second)
		}
	}
	return err
}

// SendDocument uploads data and sends it as a document attachment.
func (a *Account) SendDocument(ctx context.Context, chat types.JID, data []byte, filename, mimetype, caption string) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("whatsapp: no client")
	}
	uploaded, err := a.client.Upload(ctx, data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("upload document: %w", err)
	}
	fileLength := uint64(len(data))
	doc := &waProto.DocumentMessage{
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		Mimetype:      &mimetype,
		FileName:      &filename,
		FileSHA256:    uploaded.FileSHA256,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileLength:    &fileLength,
	}
	if strings.TrimSpace(caption) != "" {
		doc.Caption = &caption
	}
	_, err = a.client.SendMessage(ctx, chat, &waProto.Message{DocumentMessage: doc})
	return err
}

// Media is one downloaded attachment. FileName is the sender's name for a
// document (may be empty); Ext is a safe extension derived from the MIME
// type; Kind is image, document, audio or video.
type Media struct {
	Kind     string
	MimeType string
	Caption  string
	FileName string
	Ext      string
	Data     []byte
}

// ErrNoMedia is returned by Download for a message without an attachment.
var ErrNoMedia = fmt.Errorf("whatsapp: message has no downloadable media")

// Download fetches a message's attachment. maxBytes > 0 refuses larger
// files up front (by the declared size) and after the fact.
func (a *Account) Download(ctx context.Context, m *waProto.Message, maxBytes int) (*Media, error) {
	if a == nil || a.client == nil {
		return nil, fmt.Errorf("whatsapp: no client")
	}
	if m == nil {
		return nil, ErrNoMedia
	}
	var dl whatsmeow.DownloadableMessage
	media := &Media{}
	var declared uint64
	switch {
	case m.ImageMessage != nil:
		dl, media.Kind = m.ImageMessage, "image"
		media.MimeType, media.Caption, declared = m.ImageMessage.GetMimetype(), m.ImageMessage.GetCaption(), m.ImageMessage.GetFileLength()
		media.Ext = ExtensionForMime(media.MimeType, ".jpg")
	case m.DocumentMessage != nil:
		dl, media.Kind = m.DocumentMessage, "document"
		media.MimeType, media.Caption, declared = m.DocumentMessage.GetMimetype(), m.DocumentMessage.GetCaption(), m.DocumentMessage.GetFileLength()
		media.FileName = strings.TrimSpace(m.DocumentMessage.GetFileName())
		media.Ext = ExtensionForMime(media.MimeType, ".pdf")
	case m.AudioMessage != nil:
		dl, media.Kind = m.AudioMessage, "audio"
		media.MimeType, declared = m.AudioMessage.GetMimetype(), m.AudioMessage.GetFileLength()
		media.Ext = ExtensionForMime(media.MimeType, ".ogg")
	case m.VideoMessage != nil:
		dl, media.Kind = m.VideoMessage, "video"
		media.MimeType, media.Caption, declared = m.VideoMessage.GetMimetype(), m.VideoMessage.GetCaption(), m.VideoMessage.GetFileLength()
		media.Ext = ExtensionForMime(media.MimeType, ".mp4")
	default:
		return nil, ErrNoMedia
	}
	if maxBytes > 0 && declared > uint64(maxBytes) { //nolint:gosec // G115: maxBytes is a small positive limit.
		return nil, fmt.Errorf("attachment is %.1fMB; the limit is %dMB", float64(declared)/(1024*1024), maxBytes/(1024*1024))
	}
	data, err := a.client.Download(ctx, dl)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", media.Kind, err)
	}
	if maxBytes > 0 && len(data) > maxBytes {
		return nil, fmt.Errorf("attachment is %.1fMB; the limit is %dMB", float64(len(data))/(1024*1024), maxBytes/(1024*1024))
	}
	media.Data = data
	media.Caption = strings.TrimSpace(media.Caption)
	return media, nil
}

// HasMedia reports whether Download would find an attachment.
func HasMedia(m *waProto.Message) bool {
	return m != nil && (m.ImageMessage != nil || m.DocumentMessage != nil || m.AudioMessage != nil || m.VideoMessage != nil)
}

// IsVoiceNote is true for audio attachments (WhatsApp voice notes arrive as
// AudioMessage with PTT set; plain audio files too, which is fine).
func IsVoiceNote(m *waProto.Message) bool { return m != nil && m.AudioMessage != nil }

// QRUpdate is one pairing-code event: Code is empty once the code expired
// or pairing finished.
type QRUpdate struct {
	Code    string
	Expires time.Time
}

// Pair runs one pairing attempt for an unpaired account: connects, streams
// QR codes to onQR as WhatsApp rotates them, and returns paired=true once
// the phone scanned one. Gives up after timeout with paired=false. The
// account stays connected on success and is disconnected otherwise.
func (a *Account) Pair(ctx context.Context, timeout time.Duration, onQR func(QRUpdate)) (bool, error) {
	if a == nil || a.client == nil {
		return false, fmt.Errorf("whatsapp: no client")
	}
	qrChan, err := a.client.GetQRChannel(ctx)
	if err != nil {
		return false, fmt.Errorf("get QR channel: %w", err)
	}
	connectDone := make(chan error, 1)
	go func() { connectDone <- a.client.Connect() }()
	select {
	case err := <-connectDone:
		if err != nil {
			return false, fmt.Errorf("connect (pre-pair): %w", err)
		}
	case <-time.After(timeout):
		a.client.Disconnect()
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
	// The deadline only covers the wait for the first QR code: once WhatsApp
	// starts issuing codes the channel itself paces the attempt (it rotates
	// codes and closes with "timeout" when the user never scans).
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	deadlineC := deadline.C
	for {
		select {
		case evt, ok := <-qrChan:
			if !ok {
				return false, nil
			}
			switch evt.Event {
			case "code":
				deadlineC = nil
				if onQR != nil {
					onQR(QRUpdate{Code: evt.Code, Expires: time.Now().Add(evt.Timeout)})
				}
			case "error":
				if onQR != nil {
					onQR(QRUpdate{})
				}
				if evt.Error != nil {
					return false, fmt.Errorf("pairing error: %w", evt.Error)
				}
				return false, fmt.Errorf("pairing error")
			case "success":
				if onQR != nil {
					onQR(QRUpdate{})
				}
				return true, nil
			case "timeout":
				if onQR != nil {
					onQR(QRUpdate{})
				}
				return false, nil
			default:
				// whatsmeow can emit terminal "err-*" statuses (e.g.
				// err-client-outdated, err-scanned-without-multidevice).
				if onQR != nil {
					onQR(QRUpdate{})
				}
				if evt.Error != nil {
					return false, fmt.Errorf("pairing failed (%s): %w", evt.Event, evt.Error)
				}
				return false, fmt.Errorf("pairing failed (%s)", evt.Event)
			}
		case <-deadlineC:
			a.client.Disconnect()
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// ExtractText is the text of an incoming message: plain, extended, or the
// caption of an image/video/document (a parent typing under a photo is the
// normal way to ask about it), unwrapping device-sent and edited messages.
func ExtractText(m *waProto.Message) string {
	if m == nil {
		return ""
	}
	if inner := m.GetDeviceSentMessage().GetMessage(); inner != nil {
		return ExtractText(inner)
	}
	if inner := m.GetEditedMessage().GetMessage(); inner != nil {
		return ExtractText(inner)
	}
	if m.Conversation != nil {
		return strings.TrimSpace(*m.Conversation)
	}
	if m.ExtendedTextMessage != nil && m.ExtendedTextMessage.Text != nil {
		return strings.TrimSpace(*m.ExtendedTextMessage.Text)
	}
	if m.ImageMessage != nil {
		return strings.TrimSpace(m.ImageMessage.GetCaption())
	}
	if m.VideoMessage != nil {
		return strings.TrimSpace(m.VideoMessage.GetCaption())
	}
	if m.DocumentMessage != nil {
		return strings.TrimSpace(m.DocumentMessage.GetCaption())
	}
	return ""
}

// ExtensionForMime maps a MIME type to a file extension, falling back to def.
func ExtensionForMime(mime, def string) string {
	mime = strings.ToLower(mime)
	switch {
	case strings.Contains(mime, "jpeg"), strings.Contains(mime, "jpg"):
		return ".jpg"
	case strings.Contains(mime, "png"):
		return ".png"
	case strings.Contains(mime, "webp"):
		return ".webp"
	case strings.Contains(mime, "gif"):
		return ".gif"
	case strings.Contains(mime, "pdf"):
		return ".pdf"
	case strings.Contains(mime, "ogg"), strings.Contains(mime, "opus"):
		return ".ogg"
	case strings.Contains(mime, "mp4"):
		return ".mp4"
	default:
		return def
	}
}

// Dedupe remembers message ids for a window, because whatsmeow can deliver
// the same message more than once around a reconnect.
type Dedupe struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	window time.Duration
}

func NewDedupe(window time.Duration) *Dedupe {
	return &Dedupe{seen: map[string]time.Time{}, window: window}
}

// Seen reports whether id was already recorded within the window, recording
// it if not. An empty id is never a duplicate.
func (d *Dedupe) Seen(id string) bool {
	if id == "" {
		return false
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, t := range d.seen {
		if now.Sub(t) > d.window {
			delete(d.seen, k)
		}
	}
	if _, ok := d.seen[id]; ok {
		return true
	}
	d.seen[id] = now
	return false
}

// DescribeEvent renders the lifecycle events worth a log line; "" for the
// rest (messages, receipts, presence).
func DescribeEvent(rawEvt interface{}) string {
	switch evt := rawEvt.(type) {
	case *events.Connected:
		return "connected"
	case *events.Disconnected:
		return "disconnected"
	case *events.LoggedOut:
		return fmt.Sprintf("logged out (reason=%v) — session invalid, re-pair required", evt.Reason)
	case *events.ConnectFailure:
		return fmt.Sprintf("connect failure: reason=%v message=%s", evt.Reason, evt.Message)
	case *events.ClientOutdated:
		return "client outdated — whatsmeow needs upgrading against the current WhatsApp server"
	case *events.TemporaryBan:
		return fmt.Sprintf("temporary ban: code=%v expires=%s", evt.Code, evt.Expire)
	case *events.StreamError:
		return fmt.Sprintf("stream error: code=%s", evt.Code)
	case *events.StreamReplaced:
		return "stream replaced — another client took over this session"
	case *events.PairSuccess:
		return fmt.Sprintf("pair success: id=%s platform=%s", evt.ID, evt.Platform)
	case *events.PairError:
		return fmt.Sprintf("pair error: id=%s error=%v", evt.ID, evt.Error)
	}
	return ""
}

// ExtractCaption returns the caption attached to an image, video or document.
func ExtractCaption(m *waProto.Message) string {
	if m == nil {
		return ""
	}
	switch {
	case m.ImageMessage != nil:
		return strings.TrimSpace(m.ImageMessage.GetCaption())
	case m.DocumentMessage != nil:
		return strings.TrimSpace(m.DocumentMessage.GetCaption())
	case m.VideoMessage != nil:
		return strings.TrimSpace(m.VideoMessage.GetCaption())
	}
	return ""
}

// SendTextID sends a text message and returns the ID WhatsApp assigned to it.
func (a *Account) SendTextID(ctx context.Context, chat types.JID, text string) (types.MessageID, error) {
	if a == nil || a.client == nil {
		return "", fmt.Errorf("whatsapp: no client")
	}
	resp, err := a.client.SendMessage(ctx, chat, &waProto.Message{Conversation: &text})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// ChatName returns a display name for a chat: the group subject, or the
// contact's push/full name; "" when unknown.
func (a *Account) ChatName(ctx context.Context, chat types.JID) string {
	if a == nil || a.client == nil || chat.IsEmpty() {
		return ""
	}
	if chat.Server == types.GroupServer {
		if info, err := a.client.GetGroupInfo(ctx, chat); err == nil && info != nil {
			return info.GroupName.Name
		}
		return ""
	}
	if a.client.Store != nil && a.client.Store.Contacts != nil {
		if info, err := a.client.Store.Contacts.GetContact(ctx, chat); err == nil {
			if info.PushName != "" {
				return info.PushName
			}
			return info.FullName
		}
	}
	return ""
}
