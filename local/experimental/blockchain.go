package experimental

import (
	"context"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/chain/p"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary"
)

type wallet struct {
	wallet p.Wallet

	backend p.Backend
	builder p.Builder
	signer  p.Signer
}

func newWallet(ctx context.Context, uri string) (*wallet, error) {
	kc := secp256k1fx.NewKeychain(genesis.EWOQKey)
	pCtx, _, utxos, err := primary.FetchState(ctx, uri, kc.Addresses())
	if err != nil {
		return nil, err
	}
	pClient := platformvm.NewClient(uri)
	addrs := kc.Addresses()
	pUTXOs := primary.NewChainUTXOs(constants.PlatformChainID, utxos)
	var w wallet
	w.backend = p.NewBackend(pCtx, pUTXOs, map[ids.ID]*txs.Tx{})
	w.builder = p.NewBuilder(addrs, w.backend)
	w.signer = p.NewSigner(kc, w.backend)
	w.wallet = p.NewWallet(w.builder, w.signer, pClient, w.backend)
	return &w, nil
}

func (w *wallet) reload(uri string) {
	pClient := platformvm.NewClient(uri)
	w.wallet = p.NewWallet(w.builder, w.signer, pClient, w.backend)
}

func CreateSpecificBlockchains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec, // VM name + genesis bytes
) (map[string][]string, error) {
	chainInfos, err := ln.installCustomChains(ctx, chainSpecs)
	if err != nil {
		return nil, err
	}

	if err := ln.waitForCustomChainsReady(ctx, chainInfos); err != nil {
		return nil, err
	}

	if err := ln.RegisterBlockchainAliases(ctx, chainInfos, chainSpecs); err != nil {
		return nil, err
	}

	return nil, nil
}
