package mpp

import (
	"strings"

	skywallet "github.com/sky10/sky10/pkg/wallet"
)

const (
	usdcMainnet  = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	usdcDevnet   = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"
	usdtMainnet  = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
	pyusdMainnet = "2b1kV6DkPAnxd5ixfnxCpjxmKwqjjaYmCZfHsFu24GXo"
	pyusdDevnet  = "CXk2AMBfi3TwaEL2468s6zP8xq9NxTXjp9gjMgzeUynM"
	cashMainnet  = "CASHx9KJUStyftLFWGvEVf59SGeG9sh5FfcnZMVPCASH"
	usdgMainnet  = "2u1tszSeqZ3qBWF3uNGPFc8TzMk2tdiwknnRMWGWjGWH"
	usdgDevnet   = "4F6PM96JJxngmHnZLBh9n58RH4aTVNWvDs2nuwrT5BP7"
)

// ResolveSolanaMint maps common stablecoin symbols to mint addresses. Unknown
// strings pass through so callers can use explicit mint addresses. Native SOL
// returns native=true.
func ResolveSolanaMint(currency, network string) (mint string, native bool) {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "SOL":
		return "", true
	case "USDC":
		if network == "devnet" || network == "testnet" || network == "localnet" {
			return usdcDevnet, false
		}
		return usdcMainnet, false
	case "USDT":
		return usdtMainnet, false
	case "PYUSD":
		if network == "devnet" || network == "testnet" || network == "localnet" {
			return pyusdDevnet, false
		}
		return pyusdMainnet, false
	case "CASH":
		return cashMainnet, false
	case "USDG":
		if network == "devnet" || network == "testnet" || network == "localnet" {
			return usdgDevnet, false
		}
		return usdgMainnet, false
	default:
		return strings.TrimSpace(currency), false
	}
}

// DefaultTokenProgramForCurrency returns the default Solana token program for
// known stablecoins. Unknown tokens default to legacy SPL Token unless the
// challenge supplies methodDetails.tokenProgram.
func DefaultTokenProgramForCurrency(currency, network string) string {
	mint, native := ResolveSolanaMint(currency, network)
	if native {
		return ""
	}
	switch mint {
	case pyusdMainnet, pyusdDevnet, cashMainnet, usdgMainnet, usdgDevnet:
		return skywallet.SolanaToken2022Program
	default:
		return skywallet.SolanaTokenProgram
	}
}
