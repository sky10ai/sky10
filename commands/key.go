package commands

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sky10/sky10/pkg/config"
	"github.com/sky10/sky10/pkg/key"
	"github.com/spf13/cobra"
)

func KeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Key management",
	}

	cmd.AddCommand(keyGenerateCmd())
	cmd.AddCommand(keyAddressCmd())
	cmd.AddCommand(keySealCmd())
	cmd.AddCommand(keyOpenCmd())
	cmd.AddCommand(keySignCmd())
	cmd.AddCommand(keyVerifyCmd())
	cmd.AddCommand(keyExportCmd())
	cmd.AddCommand(keyImportCmd())

	return cmd
}

func keyGenerateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Generate a new keypair",
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := key.Generate()
			if err != nil {
				return err
			}
			keyPath, err := defaultKeyPath()
			if err != nil {
				return err
			}
			os.MkdirAll(filepath.Dir(keyPath), 0700)
			if err := key.SaveWithDescription(k, keyPath, "skyfs device key"); err != nil {
				return err
			}
			fmt.Printf("Generated key\n  Address: %s\n  Saved:   %s\n", k.Address(), keyPath)
			return nil
		},
	}
}

func keyAddressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "address",
		Short: "Show your sky10q... address",
		RunE: func(cmd *cobra.Command, args []string) error {
			parse, _ := cmd.Flags().GetString("parse")
			if parse != "" {
				k, err := key.ParseAddress(parse)
				if err != nil {
					return err
				}
				fmt.Printf("Public key: %s\n", hex.EncodeToString(k.PublicKey))
				return nil
			}
			k, err := loadKey()
			if err != nil {
				return err
			}
			fmt.Println(k.Address())
			return nil
		},
	}
	cmd.Flags().String("parse", "", "Decode a sky10q... address to hex public key")
	return cmd
}

func keySealCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seal <file>",
		Short: "Encrypt a file for a recipient",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			forAddr, _ := cmd.Flags().GetString("for")
			if forAddr == "" {
				return fmt.Errorf("--for sky10q... is required")
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			sealed, err := key.SealFor(data, forAddr)
			if err != nil {
				return err
			}
			outPath, _ := cmd.Flags().GetString("out")
			if outPath == "" {
				outPath = args[0] + ".sealed"
			}
			if err := os.WriteFile(outPath, sealed, 0644); err != nil {
				return err
			}
			fmt.Printf("sealed %s → %s\n", args[0], outPath)
			return nil
		},
	}
	cmd.Flags().String("for", "", "Recipient sky10q... address")
	cmd.Flags().String("out", "", "Output path")
	cmd.MarkFlagRequired("for")
	return cmd
}

func keyOpenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "open <file>",
		Short: "Decrypt a sealed file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			k, err := loadKey()
			if err != nil {
				return err
			}
			plaintext, err := key.Open(data, k.PrivateKey)
			if err != nil {
				return err
			}
			outPath, _ := cmd.Flags().GetString("out")
			if outPath == "" {
				outPath = args[0]
				if filepath.Ext(outPath) == ".sealed" {
					outPath = outPath[:len(outPath)-7]
				}
			}
			if err := os.WriteFile(outPath, plaintext, 0644); err != nil {
				return err
			}
			fmt.Printf("opened %s → %s\n", args[0], outPath)
			return nil
		},
	}
	cmd.Flags().String("out", "", "Output path")
	return cmd
}

func keySignCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sign <file>",
		Short: "Sign a file (outputs hex signature)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := loadKey()
			if err != nil {
				return err
			}
			sig, err := key.SignFile(args[0], k.PrivateKey)
			if err != nil {
				return err
			}
			fmt.Println(hex.EncodeToString(sig))
			return nil
		},
	}
}

func keyVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <file>",
		Short: "Verify a file signature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sigHex, _ := cmd.Flags().GetString("sig")
			from, _ := cmd.Flags().GetString("from")
			sig, err := hex.DecodeString(sigHex)
			if err != nil {
				return fmt.Errorf("invalid signature hex: %w", err)
			}
			signer, err := key.ParseAddress(from)
			if err != nil {
				return err
			}
			valid, err := key.VerifyFile(args[0], sig, signer.PublicKey)
			if err != nil {
				return err
			}
			if valid {
				fmt.Println("valid")
			} else {
				fmt.Println("INVALID")
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().String("sig", "", "Hex-encoded signature")
	cmd.Flags().String("from", "", "Signer's sky10q... address")
	cmd.MarkFlagRequired("sig")
	cmd.MarkFlagRequired("from")
	return cmd
}

func keyExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export",
		Short: "Show key details",
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := loadKey()
			if err != nil {
				return err
			}
			fmt.Printf("Address:     %s\n", k.Address())
			fmt.Printf("Public key:  %s\n", hex.EncodeToString(k.PublicKey))
			if k.IsPrivate() {
				fmt.Printf("Private key: %s\n", hex.EncodeToString(k.PrivateKey))
			}
			return nil
		},
	}
}

func keyImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <key-file>",
		Short: "Import a keypair",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := key.Load(args[0])
			if err != nil {
				return err
			}
			keyPath, err := defaultKeyPath()
			if err != nil {
				return err
			}
			os.MkdirAll(filepath.Dir(keyPath), 0700)
			if err := key.Save(k, keyPath); err != nil {
				return err
			}
			fmt.Printf("imported %s → %s\n", k.Address(), keyPath)
			return nil
		},
	}
}

func loadKey() (*key.Key, error) {
	keyPath, err := defaultKeyPath()
	if err != nil {
		return nil, err
	}
	k, err := key.Load(keyPath)
	if err != nil {
		return nil, fmt.Errorf("loading key: %w — run 'sky10 key generate' first", err)
	}
	return k, nil
}

func defaultKeyPath() (string, error) {
	return config.DefaultIdentityPath()
}
