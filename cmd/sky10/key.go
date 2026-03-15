package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sky10/sky10/internal/config"
	"github.com/sky10/sky10/skykey"
)

func runKey(args []string) error {
	if len(args) == 0 {
		printKeyUsage()
		return nil
	}

	switch args[0] {
	case "generate":
		return keyGenerate(args[1:])
	case "address":
		return keyAddress(args[1:])
	case "seal":
		return keySeal(args[1:])
	case "open":
		return keyOpen(args[1:])
	case "sign":
		return keySign(args[1:])
	case "verify":
		return keyVerify(args[1:])
	case "export":
		return keyExport(args[1:])
	case "import":
		return keyImport(args[1:])
	case "help", "--help", "-h":
		printKeyUsage()
		return nil
	default:
		return fmt.Errorf("unknown key command: %s", args[0])
	}
}

func keyGenerate(_ []string) error {
	k, err := skykey.Generate()
	if err != nil {
		return err
	}

	keyPath, err := defaultKeyPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := skykey.Save(k, keyPath); err != nil {
		return err
	}

	fmt.Printf("Generated key\n")
	fmt.Printf("  Address: %s\n", k.Address())
	fmt.Printf("  Saved:   %s\n", keyPath)
	return nil
}

func keyAddress(args []string) error {
	fs := flag.NewFlagSet("address", flag.ExitOnError)
	parse := fs.String("parse", "", "decode a sky10q... address to hex public key")
	fs.Parse(args)

	if *parse != "" {
		k, err := skykey.ParseAddress(*parse)
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
}

func keySeal(args []string) error {
	fs := flag.NewFlagSet("seal", flag.ExitOnError)
	forAddr := fs.String("for", "", "recipient sky10q... address (required)")
	out := fs.String("out", "", "output path (default: <input>.sealed)")
	fs.Parse(args)

	if fs.NArg() < 1 || *forAddr == "" {
		return fmt.Errorf("usage: sky10 key seal <file> --for sky10q...")
	}

	inPath := fs.Arg(0)
	data, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}

	sealed, err := skykey.SealFor(data, *forAddr)
	if err != nil {
		return err
	}

	outPath := *out
	if outPath == "" {
		outPath = inPath + ".sealed"
	}
	if err := os.WriteFile(outPath, sealed, 0644); err != nil {
		return err
	}

	fmt.Printf("sealed %s → %s (for %s...)\n", inPath, outPath, (*forAddr)[:16])
	return nil
}

func keyOpen(args []string) error {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	out := fs.String("out", "", "output path (default: strip .sealed extension)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: sky10 key open <file.sealed>")
	}

	inPath := fs.Arg(0)
	data, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}

	k, err := loadKey()
	if err != nil {
		return err
	}

	plaintext, err := skykey.Open(data, k.PrivateKey)
	if err != nil {
		return err
	}

	outPath := *out
	if outPath == "" {
		outPath = inPath
		if filepath.Ext(outPath) == ".sealed" {
			outPath = outPath[:len(outPath)-7]
		}
	}
	if err := os.WriteFile(outPath, plaintext, 0644); err != nil {
		return err
	}

	fmt.Printf("opened %s → %s\n", inPath, outPath)
	return nil
}

func keySign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: sky10 key sign <file>")
	}

	k, err := loadKey()
	if err != nil {
		return err
	}

	sig, err := skykey.SignFile(fs.Arg(0), k.PrivateKey)
	if err != nil {
		return err
	}

	fmt.Println(hex.EncodeToString(sig))
	return nil
}

func keyVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	sigHex := fs.String("sig", "", "hex-encoded signature (required)")
	from := fs.String("from", "", "signer's sky10q... address (required)")
	fs.Parse(args)

	if fs.NArg() < 1 || *sigHex == "" || *from == "" {
		return fmt.Errorf("usage: sky10 key verify <file> --sig <hex> --from sky10q...")
	}

	sig, err := hex.DecodeString(*sigHex)
	if err != nil {
		return fmt.Errorf("invalid signature hex: %w", err)
	}

	signer, err := skykey.ParseAddress(*from)
	if err != nil {
		return err
	}

	valid, err := skykey.VerifyFile(fs.Arg(0), sig, signer.PublicKey)
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
}

func keyExport(_ []string) error {
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
}

func keyImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	fs.Parse(args)

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: sky10 key import <key-file>")
	}

	k, err := skykey.Load(fs.Arg(0))
	if err != nil {
		return err
	}

	keyPath, err := defaultKeyPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(keyPath)
	os.MkdirAll(dir, 0700)

	if err := skykey.Save(k, keyPath); err != nil {
		return err
	}

	fmt.Printf("imported %s → %s\n", k.Address(), keyPath)
	return nil
}

func loadKey() (*skykey.Key, error) {
	keyPath, err := defaultKeyPath()
	if err != nil {
		return nil, err
	}
	k, err := skykey.Load(keyPath)
	if err != nil {
		return nil, fmt.Errorf("loading key: %w — run 'sky10 key generate' first", err)
	}
	return k, nil
}

func defaultKeyPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "key.json"), nil
}

func printKeyUsage() {
	fmt.Println(`sky10 key — key management

Commands:
  generate                            Generate a new keypair
  address                             Show your sky10q... address
  address --parse sky10q...           Decode an address to hex public key
  seal <file> --for sky10q...         Encrypt a file for a recipient
  open <file>                         Decrypt a sealed file
  sign <file>                         Sign a file (outputs hex signature)
  verify <file> --sig <hex> --from sky10q...  Verify a signature
  export                              Show key details
  import <key-file>                   Import a keypair`)
}
