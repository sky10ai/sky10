package commands

import (
	"context"
	"net/http"
	"time"

	skyllm "github.com/sky10/sky10/pkg/ai/llm"
	skywallet "github.com/sky10/sky10/pkg/wallet"
	"github.com/sky10/sky10/pkg/x402/siwx"
)

func newVeniceBalanceProvider() skyllm.VeniceBalanceProvider {
	client := skywallet.NewClient()
	if client == nil {
		return skyllm.VeniceBalanceProviderFunc(func(context.Context, skyllm.Connection) (*skyllm.VeniceBalanceResult, error) {
			return nil, skywallet.ErrNotInstalled
		})
	}
	return &skyllm.VeniceBalanceClient{
		AddressResolver: client,
		Signer: func(walletName string) siwx.Signer {
			return siwx.NewOWSSigner(client, walletName)
		},
		HTTP: &http.Client{Timeout: 15 * time.Second},
	}
}
