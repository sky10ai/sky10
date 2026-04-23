package commands

import (
	"strings"
	"testing"
)

func TestMessagingCmdRegistersBuiltInAdapters(t *testing.T) {
	t.Parallel()

	cmd := MessagingCmd()
	if !cmd.Hidden {
		t.Fatal("MessagingCmd().Hidden = false, want true")
	}

	got := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		got = append(got, sub.Name())
		if !sub.Hidden {
			t.Fatalf("subcommand %q Hidden = false, want true", sub.Name())
		}
	}
	want := []string{"imap-smtp", "slack", "telegram"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("MessagingCmd() subcommands = %v, want %v", got, want)
	}
}

func TestMessagingCmdExecutesAdapterStub(t *testing.T) {
	t.Parallel()

	cmd := MessagingCmd()
	cmd.SetArgs([]string{"imap-smtp"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `messaging adapter "imap-smtp" is not implemented yet`) {
		t.Fatalf("Execute() error = %v, want not implemented error", err)
	}
}
