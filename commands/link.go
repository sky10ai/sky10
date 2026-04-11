package commands

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// LinkCmd returns the `sky10 link` command group.
func LinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "P2P agent communication (skylink)",
	}
	cmd.AddCommand(linkStatusCmd())
	cmd.AddCommand(linkPeersCmd())
	cmd.AddCommand(linkConnectCmd())
	cmd.AddCommand(linkCallCmd())
	cmd.AddCommand(linkResolveCmd())
	cmd.AddCommand(linkPublishCmd())
	cmd.AddCommand(linkNetcheckCmd())
	return cmd
}

func linkStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show skylink node status",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skylink.status", nil)
			if err != nil {
				return err
			}
			var status struct {
				PeerID       string   `json:"peer_id"`
				Address      string   `json:"address"`
				Mode         string   `json:"mode"`
				Addrs        []string `json:"addrs"`
				Peers        int      `json:"peers"`
				PrivatePeers int      `json:"private_peers"`
				Health       struct {
					PreferredTransport      string `json:"preferred_transport"`
					TransportDegradedReason string `json:"transport_degraded_reason"`
					DeliveryDegradedReason  string `json:"delivery_degraded_reason"`
					Reachability            string `json:"reachability"`
					PublicAddr              string `json:"public_addr"`
					Mailbox                 struct {
						Queued              int    `json:"queued"`
						Failed              int    `json:"failed"`
						HandedOff           int    `json:"handed_off"`
						PendingPrivate      int    `json:"pending_private"`
						PendingSky10Network int    `json:"pending_sky10_network"`
						LastHandoffAt       string `json:"last_handoff_at"`
						LastDeliveredAt     string `json:"last_delivered_at"`
						LastFailureAt       string `json:"last_failure_at"`
					} `json:"mailbox"`
				} `json:"health"`
			}
			if err := json.Unmarshal(result, &status); err != nil {
				return err
			}
			fmt.Printf("identity: %s\n", status.Address)
			fmt.Printf("peer id:  %s\n", status.PeerID)
			fmt.Printf("mode:     %s\n", status.Mode)
			fmt.Printf("peers:    %d\n", status.Peers)
			fmt.Printf("private:  %d\n", status.PrivatePeers)
			if status.Health.PreferredTransport != "" {
				fmt.Printf("path:     %s\n", status.Health.PreferredTransport)
			}
			if status.Health.Reachability != "" {
				fmt.Printf("reach:    %s\n", status.Health.Reachability)
			}
			if status.Health.PublicAddr != "" {
				fmt.Printf("public:   %s\n", status.Health.PublicAddr)
			}
			if status.Health.TransportDegradedReason != "" {
				fmt.Printf("degrade:  %s\n", status.Health.TransportDegradedReason)
			}
			if status.Health.DeliveryDegradedReason != "" {
				fmt.Printf("delivery: %s\n", status.Health.DeliveryDegradedReason)
			}
			if status.Health.Mailbox.PendingPrivate > 0 || status.Health.Mailbox.PendingSky10Network > 0 || status.Health.Mailbox.Failed > 0 {
				fmt.Printf("mailbox:  queued=%d failed=%d private=%d sky10=%d handed_off=%d\n",
					status.Health.Mailbox.Queued,
					status.Health.Mailbox.Failed,
					status.Health.Mailbox.PendingPrivate,
					status.Health.Mailbox.PendingSky10Network,
					status.Health.Mailbox.HandedOff,
				)
			}
			for _, a := range status.Addrs {
				fmt.Printf("listen:   %s\n", a)
			}
			return nil
		},
	}
}

func linkConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect <address>",
		Short: "Connect to a peer by sky10 address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := rpcCall("skylink.connect", map[string]string{"address": args[0]})
			if err != nil {
				return err
			}
			fmt.Println("connected")
			return nil
		},
	}
}

func linkCallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "call <address> <method> [params-json]",
		Short: "Call a capability on a remote peer",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]interface{}{
				"address": args[0],
				"method":  args[1],
			}
			if len(args) > 2 {
				params["params"] = json.RawMessage(args[2])
			}
			result, err := rpcCall("skylink.call", params)
			if err != nil {
				return err
			}
			fmt.Println(string(result))
			return nil
		},
	}
}

func linkResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve <address>",
		Short: "Resolve a sky10 address to peer info",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skylink.resolve", map[string]string{"address": args[0]})
			if err != nil {
				return err
			}
			// Pretty print JSON.
			var pretty json.RawMessage
			if err := json.Unmarshal(result, &pretty); err != nil {
				fmt.Println(string(result))
				return nil
			}
			out, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}
}

func linkPublishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "publish",
		Short: "Publish agent record to DHT",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := rpcCall("skylink.publish", nil)
			if err != nil {
				return err
			}
			fmt.Println("published")
			return nil
		},
	}
}

func linkNetcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "netcheck",
		Short: "Probe public STUN servers for current UDP reachability",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skylink.netcheck", nil)
			if err != nil {
				return err
			}
			var netcheck struct {
				CheckedAt             string `json:"checked_at"`
				UDP                   bool   `json:"udp"`
				PublicAddr            string `json:"public_addr"`
				PreferredServer       string `json:"preferred_server"`
				MappingVariesByServer bool   `json:"mapping_varies_by_server"`
				Probes                []struct {
					Server     string `json:"server"`
					PublicAddr string `json:"public_addr"`
					LatencyMS  int64  `json:"latency_ms"`
					Error      string `json:"error"`
				} `json:"probes"`
			}
			if err := json.Unmarshal(result, &netcheck); err != nil {
				return err
			}

			fmt.Printf("udp:       %t\n", netcheck.UDP)
			if netcheck.PublicAddr != "" {
				fmt.Printf("public:    %s\n", netcheck.PublicAddr)
			}
			if netcheck.PreferredServer != "" {
				fmt.Printf("preferred: %s\n", netcheck.PreferredServer)
			}
			fmt.Printf("varying:   %t\n", netcheck.MappingVariesByServer)
			for _, probe := range netcheck.Probes {
				if probe.Error != "" {
					fmt.Printf("probe:     %s  error=%s\n", probe.Server, probe.Error)
					continue
				}
				fmt.Printf("probe:     %s  %s  %dms\n", probe.Server, probe.PublicAddr, probe.LatencyMS)
			}
			return nil
		},
	}
}

func linkPeersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "peers",
		Short: "List connected peers",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("skylink.peers", nil)
			if err != nil {
				return err
			}
			var peers struct {
				Peers []struct {
					PeerID  string `json:"peer_id"`
					Address string `json:"address"`
				} `json:"peers"`
				Count int `json:"count"`
			}
			if err := json.Unmarshal(result, &peers); err != nil {
				return err
			}
			if peers.Count == 0 {
				fmt.Println("no connected peers")
				return nil
			}
			for _, p := range peers.Peers {
				if p.Address != "" {
					fmt.Printf("%s  %s\n", p.Address, p.PeerID)
				} else {
					fmt.Println(p.PeerID)
				}
			}
			return nil
		},
	}
}
