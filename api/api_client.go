package api

import (
	"fmt"
	"time"

	"github.com/ava-labs/avalanchego/api/admin"
	"github.com/ava-labs/avalanchego/api/health"
	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/api/ipcs"
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
	platform     *platformvm.Client
	xChain       *avm.Client
	xChainWallet *avm.WalletClient
	cChain       *evm.Client
	cChainEth    *EthClient
	info         *info.Client
	health       *health.Client
	ipcs         *ipcs.Client
	keystore     *keystore.Client
	admin        *admin.Client
	pindex       *indexer.Client
	cindex       *indexer.Client
}

// Returns a new API client for a node at [ipAddr]:[port].
type NewAPIClientF func(ipAddr string, port uint, requestTimeout time.Duration) Client

// NewAPIClient initialize most of avalanchego apis
func NewAPIClient(ipAddr string, port uint, requestTimeout time.Duration) Client {
	uri := fmt.Sprintf("http://%s:%d", ipAddr, port)
	return &APIClient{
		platform:     platformvm.NewClient(uri, requestTimeout),
		xChain:       avm.NewClient(uri, "X", requestTimeout),
		xChainWallet: avm.NewWalletClient(uri, "X", requestTimeout),
		cChain:       evm.NewCChainClient(uri, requestTimeout),
		cChainEth:    NewEthClient(ipAddr, port), // wrapper over ethclient.Client
		info:         info.NewClient(uri, requestTimeout),
		health:       health.NewClient(uri, requestTimeout),
		ipcs:         ipcs.NewClient(uri, requestTimeout),
		keystore:     keystore.NewClient(uri, requestTimeout),
		admin:        admin.NewClient(uri, requestTimeout),
		pindex:       indexer.NewClient(uri, "/ext/index/P/block", requestTimeout),
		cindex:       indexer.NewClient(uri, "/ext/index/C/block", requestTimeout),
	}
}

func (c APIClient) PChainAPI() *platformvm.Client {
	return c.platform
}

func (c APIClient) XChainAPI() *avm.Client {
	return c.xChain
}

func (c APIClient) XChainWalletAPI() *avm.WalletClient {
	return c.xChainWallet
}

func (c APIClient) CChainAPI() *evm.Client {
	return c.cChain
}

func (c APIClient) CChainEthAPI() *EthClient {
	return c.cChainEth
}

func (c APIClient) InfoAPI() *info.Client {
	return c.info
}

func (c APIClient) HealthAPI() *health.Client {
	return c.health
}

func (c APIClient) IpcsAPI() *ipcs.Client {
	return c.ipcs
}

func (c APIClient) KeystoreAPI() *keystore.Client {
	return c.keystore
}

func (c APIClient) AdminAPI() *admin.Client {
	return c.admin
}

func (c APIClient) PChainIndexAPI() *indexer.Client {
	return c.pindex
}

func (c APIClient) CChainIndexAPI() *indexer.Client {
	return c.cindex
}
