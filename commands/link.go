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
				PeerID  string   `json:"peer_id"`
				Address string   `json:"address"`
				Mode    string   `json:"mode"`
				Addrs   []string `json:"addrs"`
				Peers   int      `json:"peers"`
			}
			if err := json.Unmarshal(result, &status); err != nil {
				return err
			}
			fmt.Printf("peer id:  %s\n", status.PeerID)
			fmt.Printf("address:  %s\n", status.Address)
			fmt.Printf("mode:     %s\n", status.Mode)
			fmt.Printf("peers:    %d\n", status.Peers)
			for _, a := range status.Addrs {
				fmt.Printf("listen:   %s\n", a)
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
