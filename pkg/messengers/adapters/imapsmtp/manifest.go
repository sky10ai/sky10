package imapsmtp

import (
	"github.com/sky10/sky10/pkg/messaging"
	messagingexternal "github.com/sky10/sky10/pkg/messaging/external"
)

const (
	displayName = "IMAP/SMTP"
	summary     = "Built-in IMAP/SMTP messaging adapter"
)

// adapterMeta is the public adapter description surfaced in the settings UI.
var adapterMeta = messaging.Adapter{
	ID:          "imap-smtp",
	DisplayName: displayName,
	Description: summary,
	AuthMethods: []messaging.AuthMethod{
		messaging.AuthMethodBasic,
		messaging.AuthMethodAppPassword,
	},
	Capabilities: messaging.Capabilities{
		ReceiveMessages:   true,
		SendMessages:      true,
		CreateDrafts:      true,
		UpdateDrafts:      true,
		DeleteDrafts:      true,
		ListConversations: true,
		ListMessages:      true,
		ListContainers:    true,
		SearchMessages:    true,
		Threading:         true,
		Polling:           true,
	},
}

// adapterSettings drives the generic settings form rendered for this adapter.
// Required metadata covers a typical mailbox; credential targets feed into the
// JSON blob that imapsmtp.parseCredentialPayload expects.
var adapterSettings = []messagingexternal.Setting{
	{
		Key:         "email_address",
		Label:       "Email address",
		Kind:        messagingexternal.SettingKindText,
		Target:      messagingexternal.SettingTargetMetadata,
		Required:    true,
		Description: "The mailbox identity used for outbound mail.",
		Placeholder: "you@example.com",
	},
	{
		Key:         "display_name",
		Label:       "Display name",
		Kind:        messagingexternal.SettingKindText,
		Target:      messagingexternal.SettingTargetMetadata,
		Description: "Optional display name shown on outbound messages.",
		Placeholder: "Jane Doe",
	},
	{
		Key:         "imap_host",
		Label:       "IMAP host",
		Kind:        messagingexternal.SettingKindText,
		Target:      messagingexternal.SettingTargetMetadata,
		Required:    true,
		Description: "Hostname of the IMAP server used for inbound mail.",
		Placeholder: "imap.example.com",
	},
	{
		Key:         "imap_port",
		Label:       "IMAP port",
		Kind:        messagingexternal.SettingKindNumber,
		Target:      messagingexternal.SettingTargetMetadata,
		Description: "Optional. Defaults to 993 for TLS, 143 for STARTTLS or insecure.",
		Placeholder: "993",
	},
	{
		Key:         "imap_tls_mode",
		Label:       "IMAP TLS",
		Kind:        messagingexternal.SettingKindSelect,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     "tls",
		Description: "Transport security for IMAP.",
		Options: []messagingexternal.Option{
			{Value: "tls", Label: "Implicit TLS"},
			{Value: "starttls", Label: "STARTTLS"},
			{Value: "insecure", Label: "Insecure (no TLS)"},
		},
	},
	{
		Key:         "imap_mailbox",
		Label:       "Mailbox",
		Kind:        messagingexternal.SettingKindText,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     "INBOX",
		Description: "Mailbox folder to poll for new messages.",
		Placeholder: "INBOX",
	},
	{
		Key:         "imap_archive_mailbox",
		Label:       "Archive mailbox",
		Kind:        messagingexternal.SettingKindText,
		Target:      messagingexternal.SettingTargetMetadata,
		Description: "Optional folder used when archiving messages.",
		Placeholder: "Archive",
	},
	{
		Key:         "smtp_host",
		Label:       "SMTP host",
		Kind:        messagingexternal.SettingKindText,
		Target:      messagingexternal.SettingTargetMetadata,
		Required:    true,
		Description: "Hostname of the SMTP server used for outbound mail.",
		Placeholder: "smtp.example.com",
	},
	{
		Key:         "smtp_port",
		Label:       "SMTP port",
		Kind:        messagingexternal.SettingKindNumber,
		Target:      messagingexternal.SettingTargetMetadata,
		Description: "Optional. Defaults to 465 for TLS, 587 for STARTTLS, 25 for insecure.",
		Placeholder: "587",
	},
	{
		Key:         "smtp_tls_mode",
		Label:       "SMTP TLS",
		Kind:        messagingexternal.SettingKindSelect,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     "starttls",
		Description: "Transport security for SMTP.",
		Options: []messagingexternal.Option{
			{Value: "tls", Label: "Implicit TLS"},
			{Value: "starttls", Label: "STARTTLS"},
			{Value: "insecure", Label: "Insecure (no TLS)"},
		},
	},
	{
		Key:         "poll_limit",
		Label:       "Poll batch size",
		Kind:        messagingexternal.SettingKindNumber,
		Target:      messagingexternal.SettingTargetMetadata,
		Default:     "50",
		Description: "Maximum messages fetched per poll cycle.",
		Placeholder: "50",
	},
	{
		Key:         "username",
		Label:       "Username",
		Kind:        messagingexternal.SettingKindText,
		Target:      messagingexternal.SettingTargetCredential,
		Required:    true,
		Description: "Account username for IMAP and SMTP. Often the email address.",
		Placeholder: "you@example.com",
		Secret:      true,
	},
	{
		Key:         "password",
		Label:       "Password",
		Kind:        messagingexternal.SettingKindPassword,
		Target:      messagingexternal.SettingTargetCredential,
		Required:    true,
		Description: "Account password or app password.",
		Secret:      true,
	},
	{
		Key:         "imap_username",
		Label:       "IMAP username override",
		Kind:        messagingexternal.SettingKindText,
		Target:      messagingexternal.SettingTargetCredential,
		Description: "Optional. Overrides the shared username for IMAP only.",
		Secret:      true,
	},
	{
		Key:         "imap_password",
		Label:       "IMAP password override",
		Kind:        messagingexternal.SettingKindPassword,
		Target:      messagingexternal.SettingTargetCredential,
		Description: "Optional. Overrides the shared password for IMAP only.",
		Secret:      true,
	},
	{
		Key:         "smtp_username",
		Label:       "SMTP username override",
		Kind:        messagingexternal.SettingKindText,
		Target:      messagingexternal.SettingTargetCredential,
		Description: "Optional. Overrides the shared username for SMTP only.",
		Secret:      true,
	},
	{
		Key:         "smtp_password",
		Label:       "SMTP password override",
		Kind:        messagingexternal.SettingKindPassword,
		Target:      messagingexternal.SettingTargetCredential,
		Description: "Optional. Overrides the shared password for SMTP only.",
		Secret:      true,
	},
}

// adapterActions are the buttons rendered under the IMAP/SMTP settings form.
var adapterActions = []messagingexternal.Action{
	{
		ID:          "validate",
		Label:       "Validate settings",
		Kind:        messagingexternal.ActionKindValidateConfig,
		Description: "Check the settings without persisting a connection.",
	},
	{
		ID:          "connect",
		Label:       "Connect mailbox",
		Kind:        messagingexternal.ActionKindConnect,
		Description: "Save and start polling this mailbox.",
		Primary:     true,
	},
}
