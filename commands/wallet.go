package commands

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// WalletCmd returns the `sky10 wallet` command group.
func WalletCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wallet",
		Short: "Agent wallet (powered by OWS)",
	}

	cmd.AddCommand(walletStatusCmd())
	cmd.AddCommand(walletInstallCmd())
	cmd.AddCommand(walletCheckUpdateCmd())
	cmd.AddCommand(walletCreateCmd())
	cmd.AddCommand(walletListCmd())
	cmd.AddCommand(walletAddressCmd())
	cmd.AddCommand(walletBalanceCmd())
	cmd.AddCommand(walletDepositCmd())
	cmd.AddCommand(walletPayCmd())
	cmd.AddCommand(walletTransferCmd())

	return cmd
}

func walletStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check wallet availability",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("wallet.status", nil)
			if err != nil {
				return err
			}
			var s struct {
				Installed bool   `json:"installed"`
				Wallets   int    `json:"wallets"`
				Version   string `json:"version"`
				BinPath   string `json:"bin_path"`
			}
			json.Unmarshal(result, &s)
			if !s.Installed {
				fmt.Println("ows: not installed")
				fmt.Println("run: sky10 wallet install")
				return nil
			}
			fmt.Println("ows: installed")
			if s.Version != "" {
				fmt.Printf("version: %s\n", s.Version)
			}
			if s.BinPath != "" {
				fmt.Printf("binary:  %s\n", s.BinPath)
			}
			fmt.Printf("wallets: %d\n", s.Wallets)
			return nil
		},
	}
}

func walletInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install or update the OWS wallet binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("wallet.install", nil)
			if err != nil {
				return err
			}
			var r struct {
				Status string `json:"status"`
			}
			json.Unmarshal(result, &r)
			fmt.Printf("%s — check progress with 'sky10 wallet status'\n", r.Status)
			return nil
		},
	}
}

func walletCheckUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check-update",
		Short: "Check for OWS wallet updates",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("wallet.checkUpdate", nil)
			if err != nil {
				return err
			}
			var r struct {
				Installed bool   `json:"installed"`
				Current   string `json:"current"`
				Latest    string `json:"latest"`
				Available bool   `json:"available"`
			}
			json.Unmarshal(result, &r)
			if !r.Installed {
				fmt.Printf("not installed (latest: %s)\n", r.Latest)
				fmt.Println("run: sky10 wallet install")
				return nil
			}
			if r.Available {
				fmt.Printf("update available: %s → %s\n", r.Current, r.Latest)
				fmt.Println("run: sky10 wallet install")
			} else {
				fmt.Printf("up to date: %s\n", r.Current)
			}
			return nil
		},
	}
}

func walletCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new wallet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("wallet.create", map[string]string{
				"name": args[0],
			})
			if err != nil {
				return err
			}
			var w struct {
				Name string `json:"name"`
				ID   string `json:"id"`
			}
			json.Unmarshal(result, &w)
			fmt.Printf("created wallet %q\n", w.Name)
			if w.ID != "" {
				fmt.Printf("id: %s\n", w.ID)
			}
			return nil
		},
	}
}

func walletListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List wallets",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("wallet.list", nil)
			if err != nil {
				return err
			}
			var r struct {
				Wallets []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"wallets"`
			}
			json.Unmarshal(result, &r)
			if len(r.Wallets) == 0 {
				fmt.Println("(no wallets)")
				return nil
			}
			for _, w := range r.Wallets {
				fmt.Printf("  %s", w.Name)
				if w.ID != "" {
					fmt.Printf(" (%s)", w.ID)
				}
				fmt.Println()
			}
			return nil
		},
	}
}

func walletAddressCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "address <wallet>",
		Short: "Show Solana address for a wallet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("wallet.address", map[string]string{
				"wallet": args[0],
			})
			if err != nil {
				return err
			}
			var r struct {
				Address string `json:"address"`
				Chain   string `json:"chain"`
			}
			json.Unmarshal(result, &r)
			fmt.Println(r.Address)
			return nil
		},
	}
}

func walletBalanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "balance <wallet>",
		Short: "Show token balances for a wallet on Solana",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("wallet.balance", map[string]string{
				"wallet": args[0],
			})
			if err != nil {
				return err
			}
			var r struct {
				Address string `json:"address"`
				Chain   string `json:"chain"`
				Tokens  []struct {
					Symbol  string `json:"symbol"`
					Balance string `json:"balance"`
				} `json:"tokens"`
			}
			json.Unmarshal(result, &r)
			fmt.Printf("address: %s\n", r.Address)
			if len(r.Tokens) == 0 {
				fmt.Println("(no balances)")
				return nil
			}
			for _, t := range r.Tokens {
				fmt.Printf("  %s %s\n", t.Balance, t.Symbol)
			}
			return nil
		},
	}
}

func walletDepositCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deposit <wallet>",
		Short: "Fund a wallet via on-ramp (MoonPay)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("wallet.deposit", map[string]string{
				"wallet": args[0],
			})
			if err != nil {
				return err
			}
			var r struct {
				Address string `json:"address"`
				Chain   string `json:"chain"`
				URL     string `json:"url"`
				Status  string `json:"status"`
			}
			json.Unmarshal(result, &r)
			if r.URL != "" {
				fmt.Printf("deposit: %s\n", r.URL)
			}
			fmt.Printf("address: %s\n", r.Address)
			fmt.Printf("chain:   %s\n", r.Chain)
			if r.Status != "" {
				fmt.Printf("status:  %s\n", r.Status)
			}
			return nil
		},
	}
}

func walletPayCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pay <wallet> <url>",
		Short: "Make an x402 payment to a URL",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rpcCall("wallet.pay", map[string]string{
				"wallet": args[0],
				"url":    args[1],
			})
			if err != nil {
				return err
			}
			var r struct {
				TxHash string `json:"transaction_hash"`
				Status string `json:"status"`
				Amount string `json:"amount"`
			}
			json.Unmarshal(result, &r)
			fmt.Printf("status: %s\n", r.Status)
			if r.TxHash != "" {
				fmt.Printf("tx:     %s\n", r.TxHash)
			}
			if r.Amount != "" {
				fmt.Printf("amount: %s\n", r.Amount)
			}
			return nil
		},
	}
}

func walletTransferCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transfer <wallet> <to> <amount>",
		Short: "Send tokens to a Solana address",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			token, _ := cmd.Flags().GetString("token")
			result, err := rpcCall("wallet.transfer", map[string]string{
				"wallet": args[0],
				"to":     args[1],
				"amount": args[2],
				"token":  token,
			})
			if err != nil {
				return err
			}
			var r struct {
				TxHash string `json:"transaction_hash"`
				Status string `json:"status"`
			}
			json.Unmarshal(result, &r)
			fmt.Printf("status: %s\n", r.Status)
			if r.TxHash != "" {
				fmt.Printf("tx:     %s\n", r.TxHash)
			}
			return nil
		},
	}
	cmd.Flags().String("token", "", "Token to send (e.g. USDC). Defaults to SOL")
	return cmd
}
