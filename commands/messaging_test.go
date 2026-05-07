package commands

import "testing"

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
	want := []string{"imap-smtp", "telegram"}
	if len(got) != len(want) {
		t.Fatalf("MessagingCmd() subcommands = %v, want %v", got, want)
	}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Fatalf("MessagingCmd() subcommands = %v, want %v", got, want)
		}
	}
}
