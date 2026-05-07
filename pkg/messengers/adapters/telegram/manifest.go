package telegram

import (
	"github.com/sky10/sky10/pkg/messaging"
	messagingexternal "github.com/sky10/sky10/pkg/messaging/external"
)

const (
	displayName = "Telegram"
	summary     = "Built-in Telegram Bot API messaging adapter"
)

var adapterMeta = messaging.Adapter{
	ID:          "telegram",
	DisplayName: displayName,
	Description: summary,
	AuthMethods: []messaging.AuthMethod{
		messaging.AuthMethodBotToken,
	},
	Capabilities: messaging.Capabilities{
		ReceiveMessages:   true,
		SendMessages:      true,
		CreateDrafts:      true,
		UpdateDrafts:      true,
		DeleteDrafts:      true,
		ListConversations: true,
		ListMessages:      true,
		Threading:         true,
		Attachments:       true,
		Polling:           true,
	},
}

var adapterSettings = []messagingexternal.Setting{
	{
		Key:         credentialBotToken,
		Label:       "Bot token",
		Kind:        messagingexternal.SettingKindSecret,
		Target:      messagingexternal.SettingTargetCredential,
		Required:    true,
		Description: "Telegram bot token from BotFather.",
		Placeholder: "123456:ABC-DEF...",
		Secret:      true,
	},
	{
		Key:         metaAPIBaseURL,
		Label:       "API base URL",
		Kind:        messagingexternal.SettingKindURL,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     defaultAPIBaseURL,
		Description: "Telegram Bot API endpoint. Override for a local Bot API server.",
		Placeholder: defaultAPIBaseURL,
	},
	{
		Key:         metaPollLimit,
		Label:       "Poll batch size",
		Kind:        messagingexternal.SettingKindNumber,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     "100",
		Description: "Maximum Telegram updates fetched per poll.",
		Placeholder: "100",
	},
	{
		Key:         metaPollTimeoutSeconds,
		Label:       "Poll timeout seconds",
		Kind:        messagingexternal.SettingKindNumber,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     "10",
		Description: "Long-poll timeout used for outbound getUpdates requests.",
		Placeholder: "10",
	},
	{
		Key:         metaDownloadMedia,
		Label:       "Download media",
		Kind:        messagingexternal.SettingKindBoolean,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     "true",
		Description: "Download voice notes, audio, photos, videos, and documents into Sky10 blob storage.",
	},
	{
		Key:         metaMaxDownloadBytes,
		Label:       "Max download bytes",
		Kind:        messagingexternal.SettingKindNumber,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     "20971520",
		Description: "Largest media file Sky10 downloads through the public Bot API.",
		Placeholder: "20971520",
	},
	{
		Key:         metaDropPendingOnConnect,
		Label:       "Drop pending updates on connect",
		Kind:        messagingexternal.SettingKindBoolean,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     "false",
		Description: "Discard Telegram updates already waiting before this connection starts polling.",
	},
}

var adapterActions = []messagingexternal.Action{
	{
		ID:          "validate",
		Label:       "Validate bot",
		Kind:        messagingexternal.ActionKindValidateConfig,
		Description: "Ask Telegram to verify the bot token.",
	},
	{
		ID:          "connect",
		Label:       "Connect Telegram",
		Kind:        messagingexternal.ActionKindConnect,
		Description: "Save the bot token and start polling Telegram.",
		Primary:     true,
	},
	{
		ID:          "botfather",
		Label:       "Open BotFather",
		Kind:        messagingexternal.ActionKindOpenURL,
		Description: "Open Telegram's BotFather bot to create or manage a bot token.",
		URL:         "https://t.me/BotFather",
	},
}
