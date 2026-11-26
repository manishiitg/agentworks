package services

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const whatsappUserSessionDBName = "session.db"

// WhatsAppServiceManager owns one WhatsAppService per workspace user while
// presenting a single "whatsapp" connector to BotConversationManager.
type WhatsAppServiceManager struct {
	baseDir string

	mu               sync.RWMutex
	services         map[string]*WhatsAppService
	messageHandler   BotMessageHandler
	interaction      BotInteractionHandler
	statusProvider   BotThreadStatusFunc
	profileRouter    ProfileRouteResolver
	voiceTranscriber VoiceTranscriberFunc
	workflowAccess   WhatsAppWorkflowAccessFunc
	started          bool

	// routingLocks serializes an account's routing read-then-write sequence
	// (an explicit PUT racing the throttled GET-time default-route sync).
	// Keyed by userID, one *sync.Mutex each.
	routingLocks sync.Map

	// keyLocks serializes creating one account's service, so two concurrent
	// lookups do not both construct and start it.
	keyLocks sync.Map

	// logoutRetiredDevice logs out one retired extra phone's session
	// (tests replace it; nil = logoutWhatsAppSession).
	logoutRetiredDevice func(ctx context.Context, dbPath string) error
}

// SetWorkflowAccessFunc installs the current-user workflow visibility check
// on every device, including devices created after startup.
func (m *WhatsAppServiceManager) SetWorkflowAccessFunc(fn WhatsAppWorkflowAccessFunc) {
	m.mu.Lock()
	m.workflowAccess = fn
	services := make([]*WhatsAppService, 0, len(m.services))
	for _, svc := range m.services {
		services = append(services, svc)
	}
	m.mu.Unlock()
	for _, svc := range services {
		svc.SetWorkflowAccessFunc(fn)
	}
}

// LockAccountRouting acquires this account's routing lock and returns the
// unlock function. Callers hold it for the whole read-then-write sequence.
func (m *WhatsAppServiceManager) LockAccountRouting(userID string) func() {
	lockIface, _ := m.routingLocks.LoadOrStore(userID, &sync.Mutex{})
	lock := lockIface.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

// lockServiceKey serializes creating the service for one account key.
func (m *WhatsAppServiceManager) lockServiceKey(key string) func() {
	lockIface, _ := m.keyLocks.LoadOrStore(key, &sync.Mutex{})
	lock := lockIface.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

// ProfileRouteResolver resolves a user's default product @token (see
// WhatsAppService.SetProfileRouter); set by the server layer, which owns
// profiles.
type ProfileRouteResolver func(ctx context.Context, userID, token string) (*ProfileRoute, error)

// SetProfileRouter installs the product @token resolver for every user's
// service, present and future.
func (m *WhatsAppServiceManager) SetProfileRouter(resolver ProfileRouteResolver) {
	m.mu.Lock()
	m.profileRouter = resolver
	services := make([]*WhatsAppService, 0, len(m.services))
	keys := make([]string, 0, len(m.services))
	for key, svc := range m.services {
		services = append(services, svc)
		keys = append(keys, key)
	}
	m.mu.Unlock()
	for i, svc := range services {
		m.installProfileRouter(keys[i], svc)
	}
}

// SetVoiceTranscriber installs the on-device speech transcriber for every
// device's service, present and future.
func (m *WhatsAppServiceManager) SetVoiceTranscriber(fn VoiceTranscriberFunc) {
	m.mu.Lock()
	m.voiceTranscriber = fn
	services := make([]*WhatsAppService, 0, len(m.services))
	for _, svc := range m.services {
		services = append(services, svc)
	}
	m.mu.Unlock()
	for _, svc := range services {
		svc.SetVoiceTranscriber(fn)
	}
}

func (m *WhatsAppServiceManager) installProfileRouter(key string, svc *WhatsAppService) {
	svc.SetProfileRouter(func(ctx context.Context, token string) (*ProfileRoute, error) {
		m.mu.RLock()
		resolver := m.profileRouter
		m.mu.RUnlock()
		if resolver == nil {
			return nil, nil
		}
		return resolver(ctx, whatsappOwnerUserID(svc, key), token)
	})
}

// whatsappOwnerUserID is the account a device belongs to: its owner binding,
// or the user part of its service key before anyone has claimed it.
func whatsappOwnerUserID(svc *WhatsAppService, key string) string {
	if owner := svc.GetOwner(); owner != nil && strings.TrimSpace(owner.UserID) != "" {
		return owner.UserID
	}
	return key
}

func NewWhatsAppServiceManager(baseDir string) *WhatsAppServiceManager {
	return &WhatsAppServiceManager{
		baseDir:  baseDir,
		services: make(map[string]*WhatsAppService),
	}
}

func (m *WhatsAppServiceManager) Name() string { return "whatsapp" }

func (m *WhatsAppServiceManager) Capabilities() ChannelCapabilities {
	return ChannelCapabilities{WorkflowProgress: true}
}

func (m *WhatsAppServiceManager) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.started
}

func (m *WhatsAppServiceManager) StartListening(ctx context.Context) error {
	if m.baseDir == "" {
		return fmt.Errorf("whatsapp: session directory not configured")
	}
	if err := os.MkdirAll(m.baseDir, 0o700); err != nil {
		return fmt.Errorf("whatsapp: mkdir session directory: %w", err)
	}

	m.mu.Lock()
	m.started = true
	m.mu.Unlock()

	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return fmt.Errorf("whatsapp: read session directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		userKey := entry.Name()
		m.retireExtraWhatsAppDevices(ctx, userKey)
		if _, err := os.Stat(m.devicePath(userKey)); err == nil {
			if _, err := m.serviceForKey(ctx, userKey); err != nil {
				log.Printf("[WHATSAPP] Failed to start service for user %s: %v", userKey, err)
			}
		}
	}
	return nil
}

func (m *WhatsAppServiceManager) StopListening() {
	m.mu.RLock()
	services := make([]*WhatsAppService, 0, len(m.services))
	for _, svc := range m.services {
		services = append(services, svc)
	}
	m.mu.RUnlock()
	for _, svc := range services {
		svc.StopListening()
	}
}

// ServiceForUser is the account's WhatsApp: the one linked phone its default
// profile, routing and notifications live on.
func (m *WhatsAppServiceManager) ServiceForUser(ctx context.Context, userID, email, username string) (*WhatsAppService, error) {
	_, _ = email, username
	userKey, err := whatsappUserKey(userID)
	if err != nil {
		return nil, err
	}
	return m.serviceForKey(ctx, userKey)
}

// One person, one WhatsApp (user decision 2026-09-26): an account links a
// single phone, stored at <user>/session.db and keyed by the account in the
// service map and in managed channel ids. Accounts once could link extra
// phones under <user>/devices/<slot>/; startup logs those out
// (retireExtraWhatsAppDevices) and nothing creates them any more.
const (
	// whatsappDeviceKeySeparator marked an extra phone in a service key or
	// managed channel id ("<user>~<slot>"); such ids are refused now.
	whatsappDeviceKeySeparator = "~"
	whatsappDevicesDirName     = "devices"
)

// ErrWhatsAppOnePhone refuses a second phone on one account.
var ErrWhatsAppOnePhone = fmt.Errorf("one WhatsApp per account: unpair the linked phone first")

// WhatsAppDevice describes the account's linked (or linking) phone.
type WhatsAppDevice struct {
	Slot        string    `json:"slot"`
	Label       string    `json:"label,omitempty"`
	Paired      bool      `json:"paired"`
	Connected   bool      `json:"connected"`
	OwnJID      string    `json:"own_jid,omitempty"`
	QRAvailable bool      `json:"qr_available"`
	QRExpiresAt time.Time `json:"-"`
}

func whatsappUserKey(userID string) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", fmt.Errorf("whatsapp: user ID required")
	}
	key := sanitizeWhatsAppFileName(userID)
	if key == "" {
		return "", fmt.Errorf("whatsapp: invalid user ID %q", userID)
	}
	return key, nil
}

func (m *WhatsAppServiceManager) devicePath(userKey string) string {
	return filepath.Join(m.baseDir, userKey, whatsappUserSessionDBName)
}

// serviceForEncodedKey resolves the service a managed channel id names. An
// id minted for a retired extra phone names no service.
func (m *WhatsAppServiceManager) serviceForEncodedKey(ctx context.Context, key string) (*WhatsAppService, error) {
	if strings.Contains(key, whatsappDeviceKeySeparator) {
		return nil, fmt.Errorf("whatsapp: %q was an extra phone, which is no longer supported", key)
	}
	return m.serviceForKey(ctx, key)
}

func whatsappDeviceInfo(svc *WhatsAppService) WhatsAppDevice {
	device := WhatsAppDevice{Paired: svc.IsPaired(), Connected: svc.IsConnected()}
	if label := svc.DeviceLabel(); label != "" {
		device.Label = label
	}
	if jid := svc.OwnJID(); !jid.IsEmpty() {
		device.OwnJID = jid.String()
	}
	if code, expires := svc.GetQR(); code != "" && !expires.IsZero() {
		device.QRAvailable = true
		device.QRExpiresAt = expires
	}
	return device
}

// Devices lists the account's phone (at most one).
func (m *WhatsAppServiceManager) Devices(ctx context.Context, userID string) ([]WhatsAppDevice, error) {
	userKey, err := whatsappUserKey(userID)
	if err != nil {
		return nil, err
	}
	primary, err := m.serviceForKey(ctx, userKey)
	if err != nil {
		return nil, err
	}
	return []WhatsAppDevice{whatsappDeviceInfo(primary)}, nil
}

// SetDeviceLabel persists a display name for the account's phone. If the
// service is resident the label is set on it; otherwise it is written
// straight to the local metadata store (SetDeviceLabelOffline) instead of
// opening a WhatsApp connection just to save a name.
func (m *WhatsAppServiceManager) SetDeviceLabel(ctx context.Context, userID, label string) error {
	userKey, err := whatsappUserKey(userID)
	if err != nil {
		return err
	}
	m.mu.RLock()
	svc := m.services[userKey]
	m.mu.RUnlock()
	if svc != nil {
		return svc.SetDeviceLabel(ctx, label)
	}
	return NewWhatsAppService(m.devicePath(userKey)).SetDeviceLabelOffline(ctx, label)
}

// UnpairDevice forgets the account's phone: the pairing is reset to a fresh
// one, ready for a new scan.
func (m *WhatsAppServiceManager) UnpairDevice(ctx context.Context, userID string) error {
	userKey, err := whatsappUserKey(userID)
	if err != nil {
		return err
	}
	primary, err := m.serviceForKey(ctx, userKey)
	if err != nil {
		return err
	}
	return primary.Unpair(ctx)
}

// logoutWhatsAppSession connects a stored pairing just long enough to log it
// out of WhatsApp, then deletes its session files.
func logoutWhatsAppSession(ctx context.Context, dbPath string) error {
	svc := NewWhatsAppService(dbPath)
	if err := svc.StartListening(ctx); err != nil {
		return err
	}
	if err := svc.LogoutAndRemove(ctx); err != nil {
		svc.StopListening()
		return err
	}
	return nil
}

// retireExtraWhatsAppDevices logs out every extra phone an account linked
// before one-phone-per-account, so the phone shows the link removed, and
// deletes its session. A phone that cannot be logged out now (offline,
// WhatsApp unreachable) keeps its files and is retried at the next start,
// rather than leaving a linked device nobody can remove from the server.
func (m *WhatsAppServiceManager) retireExtraWhatsAppDevices(ctx context.Context, userKey string) {
	root := filepath.Join(m.baseDir, userKey, whatsappDevicesDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		dbPath := filepath.Join(dir, whatsappUserSessionDBName)
		if _, err := os.Stat(dbPath); err != nil {
			_ = os.RemoveAll(dir)
			continue
		}
		logout := m.logoutRetiredDevice
		if logout == nil {
			logout = logoutWhatsAppSession
		}
		if err := logout(ctx, dbPath); err != nil {
			log.Printf("[WHATSAPP] Extra phone %s of %s: logout failed (retry next start): %v", entry.Name(), userKey, err)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("[WHATSAPP] Extra phone %s of %s: logged out, but removing %s failed: %v", entry.Name(), userKey, dir, err)
			continue
		}
		log.Printf("[WHATSAPP] Logged out and removed extra phone %s of %s (one WhatsApp per account)", entry.Name(), userKey)
	}
	if remaining, err := os.ReadDir(root); err == nil && len(remaining) == 0 {
		_ = os.Remove(root)
	}
}

func (m *WhatsAppServiceManager) serviceForKey(ctx context.Context, userKey string) (*WhatsAppService, error) {
	key := userKey

	m.mu.RLock()
	svc := m.services[key]
	m.mu.RUnlock()
	if svc != nil {
		if !svc.IsEnabled() {
			if err := svc.StartListening(ctx); err != nil {
				return nil, err
			}
		}
		return svc, nil
	}

	// Not resident. Serialize creation for this key so two concurrent lookups
	// do not both construct and start a service.
	unlockKey := m.lockServiceKey(key)
	defer unlockKey()

	m.mu.RLock()
	svc = m.services[key]
	m.mu.RUnlock()
	if svc != nil {
		if !svc.IsEnabled() {
			if err := svc.StartListening(ctx); err != nil {
				return nil, err
			}
		}
		return svc, nil
	}

	dbPath := m.devicePath(userKey)
	svc = NewWhatsAppService(dbPath)
	m.configureService(key, svc)

	m.mu.Lock()
	if existing := m.services[key]; existing != nil {
		m.mu.Unlock()
		return existing, nil
	}
	m.services[key] = svc
	if err := svc.StartListening(ctx); err != nil {
		delete(m.services, key)
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	return svc, nil
}

func (m *WhatsAppServiceManager) configureService(userID string, svc *WhatsAppService) {
	m.mu.RLock()
	workflowAccess := m.workflowAccess
	m.mu.RUnlock()
	svc.SetWorkflowAccessFunc(workflowAccess)
	svc.SetMessageHandler(func(msg BotIncomingMessage) {
		rawChannelID := msg.ChannelID
		encodedChannelID := encodeWhatsAppManagedChannelID(userID, rawChannelID)
		msg.ChannelID = encodedChannelID
		if msg.ThreadTS == rawChannelID {
			msg.ThreadTS = encodedChannelID
		}
		m.mu.RLock()
		handler := m.messageHandler
		m.mu.RUnlock()
		if handler != nil {
			handler(msg)
		}
	})
	svc.SetInteractionHandler(func(threadID ThreadID, actionID, value, senderUserID string) {
		channelID := threadID.ChannelID
		threadID.ChannelID = encodeWhatsAppManagedChannelID(userID, channelID)
		if threadID.ThreadTS == "" || threadID.ThreadTS == channelID {
			threadID.ThreadTS = threadID.ChannelID
		}
		m.mu.RLock()
		handler := m.interaction
		m.mu.RUnlock()
		if handler != nil {
			handler(threadID, actionID, value, senderUserID)
		}
	})
	m.installProfileRouter(userID, svc)
	m.mu.RLock()
	transcriber := m.voiceTranscriber
	m.mu.RUnlock()
	if transcriber != nil {
		svc.SetVoiceTranscriber(transcriber)
	}
	svc.SetBotThreadStatusProvider(func(threadID ThreadID) BotThreadStatus {
		rawChannelID := threadID.ChannelID
		encodedChannelID := encodeWhatsAppManagedChannelID(userID, rawChannelID)
		threadID.ChannelID = encodedChannelID
		if threadID.ThreadTS == "" || threadID.ThreadTS == rawChannelID {
			threadID.ThreadTS = encodedChannelID
		}
		m.mu.RLock()
		provider := m.statusProvider
		m.mu.RUnlock()
		if provider == nil {
			return BotThreadStatus{DetailMode: "concise"}
		}
		return provider(threadID)
	})
}

func encodeWhatsAppManagedChannelID(userID, chatJID string) string {
	return userID + "|" + chatJID
}

func decodeWhatsAppManagedChannelID(channelID string) (userID, chatJID string, ok bool) {
	userID, chatJID, ok = strings.Cut(channelID, "|")
	return userID, chatJID, ok && userID != "" && chatJID != ""
}

func (m *WhatsAppServiceManager) serviceForThread(ctx context.Context, threadID ThreadID) (*WhatsAppService, ThreadID, error) {
	userID, chatJID, ok := decodeWhatsAppManagedChannelID(threadID.ChannelID)
	if !ok {
		return nil, threadID, fmt.Errorf("whatsapp: managed channel ID missing owner")
	}
	svc, err := m.serviceForEncodedKey(ctx, userID)
	if err != nil {
		return nil, threadID, err
	}
	threadID.ChannelID = chatJID
	if threadID.ThreadTS == "" || threadID.ThreadTS == encodeWhatsAppManagedChannelID(userID, chatJID) {
		threadID.ThreadTS = chatJID
	}
	return svc, threadID, nil
}

func (m *WhatsAppServiceManager) SendThreadMessage(ctx context.Context, threadID ThreadID, message string) (string, error) {
	svc, rawThreadID, err := m.serviceForThread(ctx, threadID)
	if err != nil {
		return "", err
	}
	return svc.SendThreadMessage(ctx, rawThreadID, message)
}

func (m *WhatsAppServiceManager) SendThreadMessageWithBlocks(ctx context.Context, threadID ThreadID, message string, blocks []MessageBlock) (string, error) {
	svc, rawThreadID, err := m.serviceForThread(ctx, threadID)
	if err != nil {
		return "", err
	}
	return svc.SendThreadMessageWithBlocks(ctx, rawThreadID, message, blocks)
}

func (m *WhatsAppServiceManager) UpdateMessage(ctx context.Context, threadID ThreadID, messageID string, newText string) error {
	svc, rawThreadID, err := m.serviceForThread(ctx, threadID)
	if err != nil {
		return err
	}
	return svc.UpdateMessage(ctx, rawThreadID, messageID, newText)
}

func (m *WhatsAppServiceManager) AddReaction(ctx context.Context, channelID, messageTS, emoji string) error {
	userID, chatJID, ok := decodeWhatsAppManagedChannelID(channelID)
	if !ok {
		return nil
	}
	svc, err := m.serviceForEncodedKey(ctx, userID)
	if err != nil {
		return err
	}
	return svc.AddReaction(ctx, chatJID, messageTS, emoji)
}

func (m *WhatsAppServiceManager) RemoveReaction(ctx context.Context, channelID, messageTS, emoji string) error {
	userID, chatJID, ok := decodeWhatsAppManagedChannelID(channelID)
	if !ok {
		return nil
	}
	svc, err := m.serviceForEncodedKey(ctx, userID)
	if err != nil {
		return err
	}
	return svc.RemoveReaction(ctx, chatJID, messageTS, emoji)
}

func (m *WhatsAppServiceManager) GetThreadHistory(ctx context.Context, threadID ThreadID) ([]ThreadMessage, error) {
	svc, rawThreadID, err := m.serviceForThread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	return svc.GetThreadHistory(ctx, rawThreadID)
}

func (m *WhatsAppServiceManager) GetChannelName(ctx context.Context, channelID string) string {
	userID, chatJID, ok := decodeWhatsAppManagedChannelID(channelID)
	if !ok {
		return ""
	}
	svc, err := m.serviceForEncodedKey(ctx, userID)
	if err != nil {
		return ""
	}
	return svc.GetChannelName(ctx, chatJID)
}

// LoadBotSessionBinding implements botSessionBindingStore. serviceForThread
// decodes the managed channel ID, so the lookup lands in exactly one
// account/device SQLite file before using the raw WhatsApp chat JID.
func (m *WhatsAppServiceManager) LoadBotSessionBinding(ctx context.Context, threadID ThreadID, routeKey string) (BotSessionBinding, bool, error) {
	svc, rawThreadID, err := m.serviceForThread(ctx, threadID)
	if err != nil {
		return BotSessionBinding{}, false, err
	}
	binding, ok := svc.loadBotSessionBinding(rawThreadID.ChannelID, routeKey)
	return binding, ok, nil
}

// SaveBotSessionBinding implements botSessionBindingStore.
func (m *WhatsAppServiceManager) SaveBotSessionBinding(ctx context.Context, threadID ThreadID, binding BotSessionBinding) error {
	svc, rawThreadID, err := m.serviceForThread(ctx, threadID)
	if err != nil {
		return err
	}
	return svc.saveBotSessionBinding(ctx, rawThreadID.ChannelID, binding)
}

// ClearBotSessionBinding implements botSessionBindingStore.
func (m *WhatsAppServiceManager) ClearBotSessionBinding(ctx context.Context, threadID ThreadID) error {
	svc, rawThreadID, err := m.serviceForThread(ctx, threadID)
	if err != nil {
		return err
	}
	return svc.clearBotSessionBinding(ctx, rawThreadID.ChannelID)
}

func (m *WhatsAppServiceManager) SetMessageHandler(handler BotMessageHandler) {
	m.mu.Lock()
	m.messageHandler = handler
	m.mu.Unlock()
}

func (m *WhatsAppServiceManager) SetInteractionHandler(handler BotInteractionHandler) {
	m.mu.Lock()
	m.interaction = handler
	m.mu.Unlock()
}

func (m *WhatsAppServiceManager) SetBotThreadStatusProvider(provider BotThreadStatusFunc) {
	m.mu.Lock()
	m.statusProvider = provider
	m.mu.Unlock()
}

func (m *WhatsAppServiceManager) GetFormatter() MessageFormatter {
	return &WhatsAppFormatter{}
}

func (m *WhatsAppServiceManager) SendNotification(ctx context.Context, uniqueID, message, contextMsg string, opts *ButtonOptions, dest *NotificationDestination) (string, error) {
	if dest == nil || strings.TrimSpace(dest.UserID) == "" {
		return "", nil
	}
	svc, err := m.ServiceForUser(ctx, dest.UserID, "", "")
	if err != nil {
		return "", err
	}
	return svc.SendNotification(ctx, uniqueID, message, contextMsg, opts, dest)
}

func (m *WhatsAppServiceManager) SendUserNotification(ctx context.Context, message, contextMsg string, dest *NotificationDestination) (string, error) {
	if dest == nil || strings.TrimSpace(dest.UserID) == "" {
		return "", nil
	}
	svc, err := m.ServiceForUser(ctx, dest.UserID, "", "")
	if err != nil {
		return "", err
	}
	return svc.SendUserNotification(ctx, message, contextMsg, dest)
}

// RoutingForUser inspects saved links without starting or reconnecting a phone.
func (m *WhatsAppServiceManager) RoutingForUser(ctx context.Context, userID string) (WhatsAppRouting, error) {
	key, err := whatsappUserKey(userID)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	svc := m.services[key]
	m.mu.RUnlock()
	if svc != nil {
		return svc.GetRouting(), nil
	}
	return NewWhatsAppService(m.devicePath(key)).routingOffline(ctx)
}

// SetRoutingForUser changes confirmed links without enabling the connector.
// The caller holds LockAccountRouting across read/modify/write.
func (m *WhatsAppServiceManager) SetRoutingForUser(ctx context.Context, userID string, routes WhatsAppRouting) error {
	key, err := whatsappUserKey(userID)
	if err != nil {
		return err
	}
	m.mu.RLock()
	svc := m.services[key]
	m.mu.RUnlock()
	if svc != nil {
		return svc.SetRouting(routes)
	}
	svc = NewWhatsAppService(m.devicePath(key))
	if err := svc.openMetaStore(ctx); err != nil {
		return err
	}
	defer svc.closeMetaStore()
	return svc.SetRouting(routes)
}
