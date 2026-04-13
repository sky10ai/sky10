package commands

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	skysecrets "github.com/sky10/sky10/pkg/secrets"
	"github.com/spf13/cobra"
)

// SecretsCmd returns the `sky10 secrets` command group.
func SecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Encrypted secrets store for API keys, tokens, and files",
	}

	cmd.AddCommand(secretsPutCmd())
	cmd.AddCommand(secretsGetCmd())
	cmd.AddCommand(secretsListCmd())
	cmd.AddCommand(secretsDevicesCmd())
	cmd.AddCommand(secretsRewrapCmd())
	cmd.AddCommand(secretsSyncCmd())
	cmd.AddCommand(secretsStatusCmd())

	return cmd
}

func secretsPutCmd() *cobra.Command {
	var (
		filePath        string
		value           string
		kind            string
		contentType     string
		recipientDevice []string
		allowedAgents   []string
		requireApproval bool
	)

	cmd := &cobra.Command{
		Use:   "put <name>",
		Short: "Store a secret value or file",
		Example: strings.TrimSpace(`
sky10 secrets put openai --value "$OPENAI_API_KEY" --kind api-key
sky10 secrets put service-cert --file cert.pem --kind cert
`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var payload []byte
			if filePath != "" && value != "" {
				return fmt.Errorf("use either --file or --value, not both")
			}
			switch {
			case filePath != "":
				data, err := os.ReadFile(filePath)
				if err != nil {
					return err
				}
				payload = data
			case value != "":
				payload = []byte(value)
			default:
				return fmt.Errorf("either --file or --value is required")
			}
			if contentType == "" {
				if value != "" {
					contentType = "text/plain; charset=utf-8"
				} else {
					contentType = "application/octet-stream"
				}
			}

			raw, err := rpcCall("secrets.put", map[string]interface{}{
				"name":              args[0],
				"kind":              kind,
				"content_type":      contentType,
				"payload":           base64.StdEncoding.EncodeToString(payload),
				"recipient_devices": recipientDevice,
				"allowed_agents":    allowedAgents,
				"require_approval":  requireApproval,
			})
			if err != nil {
				return err
			}

			var summary struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Kind    string `json:"kind"`
				Size    int64  `json:"size"`
				SHA256  string `json:"sha256"`
				Updated string `json:"updated_at"`
			}
			json.Unmarshal(raw, &summary)
			fmt.Printf("stored %s (%s, %d bytes)\n", summary.Name, summary.ID, summary.Size)
			fmt.Printf("kind:   %s\nsha256: %s\n", summary.Kind, summary.SHA256)
			return nil
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "read secret bytes from file")
	cmd.Flags().StringVar(&value, "value", "", "store literal string value")
	cmd.Flags().StringVar(&kind, "kind", skysecrets.KindBlob, "secret kind (for example: api-key, token, cert, blob)")
	cmd.Flags().StringVar(&contentType, "content-type", "", "content type override (defaults to text/plain for --value, application/octet-stream for --file)")
	cmd.Flags().StringArrayVar(&recipientDevice, "device", nil, "recipient device ID (repeatable, default current device)")
	cmd.Flags().StringArrayVar(&allowedAgents, "agent", nil, "allowed agent ID (soft policy, repeatable)")
	cmd.Flags().BoolVar(&requireApproval, "require-approval", false, "require human approval for agent-mode access")
	return cmd
}

func secretsGetCmd() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "get <id-or-name>",
		Short: "Fetch and decrypt a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := rpcCall("secrets.get", map[string]string{"id_or_name": args[0]})
			if err != nil {
				return err
			}

			var resp struct {
				Name    string `json:"name"`
				Payload string `json:"payload"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}
			payload, err := base64.StdEncoding.DecodeString(resp.Payload)
			if err != nil {
				return err
			}

			switch outPath {
			case "":
				if !isPrintable(payload) {
					return fmt.Errorf("secret is binary; use --out to write it to a file")
				}
				fmt.Print(string(payload))
				if len(payload) == 0 || payload[len(payload)-1] != '\n' {
					fmt.Println()
				}
			case "-":
				if _, err := os.Stdout.Write(payload); err != nil {
					return err
				}
			default:
				if err := os.WriteFile(outPath, payload, 0600); err != nil {
					return err
				}
				fmt.Printf("wrote %s\n", outPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "write decrypted bytes to file ('-' for stdout)")
	return cmd
}

func secretsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secrets visible to this device",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := rpcCall("secrets.list", nil)
			if err != nil {
				return err
			}
			var resp struct {
				Items []struct {
					ID                 string   `json:"id"`
					Name               string   `json:"name"`
					Kind               string   `json:"kind"`
					RecipientDeviceIDs []string `json:"recipient_device_ids"`
				} `json:"items"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}
			if len(resp.Items) == 0 {
				fmt.Println("(no secrets)")
				return nil
			}
			for _, item := range resp.Items {
				fmt.Printf("%s\t%s\t%s\t%s\n", item.ID, item.Name, item.Kind, strings.Join(item.RecipientDeviceIDs, ","))
			}
			return nil
		},
	}
}

func secretsDevicesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "devices",
		Short: "List devices that can receive secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := rpcCall("secrets.devices", nil)
			if err != nil {
				return err
			}
			var resp struct {
				Devices []struct {
					ID      string `json:"id"`
					Name    string `json:"name"`
					Current bool   `json:"current"`
				} `json:"devices"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}
			for _, dev := range resp.Devices {
				label := dev.Name
				if dev.Current {
					label += " (current)"
				}
				fmt.Printf("%s\t%s\n", dev.ID, label)
			}
			return nil
		},
	}
}

func secretsRewrapCmd() *cobra.Command {
	var (
		recipientDevice []string
		allowedAgents   []string
		requireApproval bool
	)
	cmd := &cobra.Command{
		Use:   "rewrap <id-or-name>",
		Short: "Rotate a secret to a fresh key and recipient set",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := rpcCall("secrets.rewrap", map[string]interface{}{
				"id_or_name":        args[0],
				"recipient_devices": recipientDevice,
				"allowed_agents":    allowedAgents,
				"require_approval":  requireApproval,
			})
			if err != nil {
				return err
			}
			var summary struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			json.Unmarshal(raw, &summary)
			fmt.Printf("rewrapped %s (%s)\n", summary.Name, summary.ID)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&recipientDevice, "device", nil, "recipient device ID (repeatable)")
	cmd.Flags().StringArrayVar(&allowedAgents, "agent", nil, "allowed agent ID (soft policy, repeatable)")
	cmd.Flags().BoolVar(&requireApproval, "require-approval", false, "require human approval for agent-mode access")
	return cmd
}

func secretsSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Sync secrets with remote devices",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := rpcCall("secrets.sync", nil); err != nil {
				return err
			}
			fmt.Println("synced")
			return nil
		},
	}
}

func secretsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show secrets store status",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := rpcCall("secrets.status", nil)
			if err != nil {
				return err
			}
			var resp struct {
				Namespace string `json:"namespace"`
				DeviceID  string `json:"device_id"`
				Count     int    `json:"count"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}
			fmt.Printf("namespace: %s\ndevice:    %s\ncount:     %d\n", resp.Namespace, resp.DeviceID, resp.Count)
			return nil
		},
	}
}

func isPrintable(data []byte) bool {
	for _, b := range data {
		if b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b < 0x20 || b > 0x7e {
			return false
		}
	}
	return true
}
