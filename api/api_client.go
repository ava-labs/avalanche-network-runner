package api

import (
	"fmt"

	"github.com/ava-labs/avalanchego/api/admin"
	"github.com/ava-labs/avalanchego/api/health"
	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/api/keystore"
	"github.com/ava-labs/avalanchego/indexer"
	"github.com/ava-labs/avalanchego/vms/avm"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/coreth/plugin/evm"
)

// interface compliance
var (
	_ Client        = (*APIClient)(nil)
	_ NewAPIClientF = NewAPIClient
)

// APIClient gives access to most avalanchego apis (or suitable wrappers)
type APIClient struct {
	platform     platformvm.Client
	xChain       avm.Client
	xChainWallet avm.WalletClient
	cChain       evm.Client
	cChainEth    EthClient
	info         info.Client
	health       health.Client
	keystore     keystore.Client
	admin        admin.Client
	pindex       indexer.Client
	cindex       indexer.Client
}

// Returns a new API client for a node at [ipAddr]:[port].
type NewAPIClientF func(ipAddr string, port uint16) Client

// NewAPIClient initialize most of avalanchego apis
func NewAPIClient(ipAddr string, port uint16) Client {
	uri := fmt.Sprintf("http://%s:%d", ipAddr, port)
	return &APIClient{
		platform:     platformvm.NewClient(uri),
		xChain:       avm.NewClient(uri, "X"),
		xChainWallet: avm.NewWalletClient(uri, "X"),
		cChain:       evm.NewCChainClient(uri),
		cChainEth:    NewEthClient(ipAddr, uint(port)), // wrapper over ethclient.Client
		info:         info.NewClient(uri),
		health:       health.NewClient(uri),
		keystore:     keystore.NewClient(uri),
		admin:        admin.NewClient(uri),
		pindex:       indexer.NewClient(uri + "/ext/index/P/block"),
		cindex:       indexer.NewClient(uri + "/ext/index/C/block"),
	}
}

func (c APIClient) PChainAPI() platformvm.Client {
	return c.platform
}

func (c APIClient) XChainAPI() avm.Client {
	return c.xChain
}

func (c APIClient) XChainWalletAPI() avm.WalletClient {
	return c.xChainWallet
}

func (c APIClient) CChainAPI() evm.Client {
	return c.cChain
}

func (c APIClient) CChainEthAPI() EthClient {
	return c.cChainEth
}

func (c APIClient) InfoAPI() info.Client {
	return c.info
}

func (c APIClient) HealthAPI() health.Client {
	return c.health
}

func (c APIClient) KeystoreAPI() keystore.Client {
	return c.keystore
}

func (c APIClient) AdminAPI() admin.Client {
	return c.admin
}

func (c APIClient) PChainIndexAPI() indexer.Client {
	return c.pindex
}

func (c APIClient) CChainIndexAPI() indexer.Client {
	return c.cindex
}
