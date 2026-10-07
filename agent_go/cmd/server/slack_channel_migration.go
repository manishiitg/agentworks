package server

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// Explicit Slack channels (PLAT-668, owner 2026-10-07: "we should always just
// direct add which channels the bot answers to"). A bot answers only in
// channels added on a target's Slack tab. Before this, an own bot answered for
// its own target in any channel it was in; the one-time migration below lists
// every such channel on the bot's own target so nothing that worked stops, and
// the old fallback stays on for a bot until its migration has run.

func slackChannelMigrationPath() string { return "config/slack-channel-migration.json" }

// slackChannelMigration records the bots whose channels are explicit.
type slackChannelMigration struct {
	Done map[string]time.Time `json:"done"`
}

var slackChannelMigrationMu sync.Mutex

func loadSlackChannelMigration(ctx context.Context) slackChannelMigration {
	record := slackChannelMigration{Done: map[string]time.Time{}}
	raw, found, err := readFileFromWorkspace(ctx, slackChannelMigrationPath())
	if err != nil || !found || strings.TrimSpace(raw) == "" {
		return record
	}
	if json.Unmarshal([]byte(raw), &record) != nil || record.Done == nil {
		record.Done = map[string]time.Time{}
	}
	return record
}

// slackChannelsMigrated reports whether a bot answers only in its listed
// channels (its migration ran, or it was created after the change).
func slackChannelsMigrated(ctx context.Context, connectionID string) bool {
	_, done := loadSlackChannelMigration(ctx).Done[strings.TrimSpace(connectionID)]
	return done
}

// markSlackChannelsMigrated records a bot as explicit-channels only.
func markSlackChannelsMigrated(ctx context.Context, connectionID string) error {
	slackChannelMigrationMu.Lock()
	defer slackChannelMigrationMu.Unlock()
	record := loadSlackChannelMigration(ctx)
	record.Done[strings.TrimSpace(connectionID)] = time.Now().UTC()
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, slackChannelMigrationPath(), string(raw))
}

// slackBotChannelLister lists the channels a bot is in; tests replace it.
var slackBotChannelLister = func(ctx context.Context, svc *services.SlackService, connectionID string) ([]services.SlackChannelInfo, error) {
	runtime, err := svc.ServiceForConnection(connectionID)
	if err != nil {
		return nil, err
	}
	return runtime.BotChannels(ctx)
}

// migrateSlackOwnBotChannels lists, once per own bot, every channel the bot
// is in that it does not list yet on the bot's own target (the access the
// old fallback gave: Run mode as the route). A bot Slack cannot be asked
// about is retried on the next start and keeps the fallback until then.
func (api *StreamingAPI) migrateSlackOwnBotChannels(ctx context.Context, svc *services.SlackService) {
	if svc == nil {
		return
	}
	for _, conn := range svc.ListConnections() {
		if strings.TrimSpace(conn.WorkspacePath) == "" || !conn.Enabled || slackChannelsMigrated(ctx, conn.ID) {
			continue
		}
		own := services.SlackTargetRef{WorkspacePath: conn.WorkspacePath, ProfileID: conn.ProfileID}
		if own.IsCode() {
			// A Code never answered in channels.
			if err := markSlackChannelsMigrated(ctx, conn.ID); err != nil {
				log.Printf("[SLACK_CHANNEL_MIGRATION] %s: record: %v", conn.ID, err)
			}
			continue
		}
		channels, err := slackBotChannelLister(ctx, svc, conn.ID)
		if err != nil {
			log.Printf("[SLACK_CHANNEL_MIGRATION] %s (%s): Slack unavailable, retrying on the next start: %v", conn.ID, conn.DisplayName, err)
			continue
		}
		var added []string
		_, err = svc.ModifySlackConnection(ctx, conn.ID, func(c *services.SlackConnection) error {
			for _, channel := range channels {
				id := services.NormalizeSlackChannelID(channel.ID)
				if id == "" {
					continue
				}
				if _, listed := c.ChannelRoutes[id]; listed {
					continue
				}
				c.ChannelRoutes[id] = services.SlackConnectionRoute{WorkspacePath: own.WorkspacePath, ProfileID: own.ProfileID, AddedBy: "migration"}
				added = append(added, id+" #"+channel.Name)
			}
			return nil
		})
		if err != nil {
			log.Printf("[SLACK_CHANNEL_MIGRATION] %s (%s): saving channels failed, retrying on the next start: %v", conn.ID, conn.DisplayName, err)
			continue
		}
		for _, channel := range added {
			log.Printf("[SLACK_CHANNEL_MIGRATION] %s (%s): %s now listed for %s", conn.ID, conn.DisplayName, channel, own.WorkspacePath)
		}
		if err := markSlackChannelsMigrated(ctx, conn.ID); err != nil {
			log.Printf("[SLACK_CHANNEL_MIGRATION] %s: record: %v", conn.ID, err)
			continue
		}
		log.Printf("[SLACK_CHANNEL_MIGRATION] %s (%s): done, %d channel(s) listed", conn.ID, conn.DisplayName, len(added))
	}
}
