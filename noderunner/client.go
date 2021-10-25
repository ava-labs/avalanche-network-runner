package avalanchegoclient

import (
	"fmt"
	"time"

    "github.com/sirupsen/logrus"
	"github.com/ava-labs/avalanchego/api/admin"
	"github.com/ava-labs/avalanchego/api/health"
	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/api/ipcs"
	"github.com/ava-labs/avalanchego/api/keystore"
	"github.com/ava-labs/avalanchego/indexer"
	"github.com/ava-labs/avalanchego/vms/avm"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/coreth/ethclient"
	"github.com/ava-labs/coreth/plugin/evm"
)

// Chain names
const (
	xChainPrefix = "X"
    TimeoutDuration = 30 * time.Second
)

// Client is a general client for avalanche
type Client struct {
	admin               *admin.Client
	xChain              *avm.Client
	xChainWallet        *avm.WalletClient
	health              *health.Client
	info                *info.Client
	cindex              *indexer.Client
	pindex              *indexer.Client
	ipcs                *ipcs.Client
	keystore            *keystore.Client
	platform            *platformvm.Client
	cChain              *evm.Client
	cChainEth           *ethclient.Client
	cChainConcurrentEth *ConcurrentEthClient
	byzantine           *ByzantineClient
	ipAddr              string
	port                uint
}

// NewClient returns a Client for interacting with the Chain endpoints
func NewClient(ipAddr string, port uint, byzPort uint, requestTimeout time.Duration) *Client {
	uri := fmt.Sprintf("http://%s:%d", ipAddr, port)
	byzURI := fmt.Sprintf("http://%s:%d", ipAddr, byzPort)

	return &Client{
		ipAddr:              ipAddr,
		port:                port,
		admin:               admin.NewClient(uri, requestTimeout),
		xChain:              avm.NewClient(uri, xChainPrefix, requestTimeout),
		xChainWallet:        avm.NewWalletClient(uri, xChainPrefix, requestTimeout),
		health:              health.NewClient(uri, requestTimeout),
		info:                info.NewClient(uri, requestTimeout),
		cindex:              indexer.NewClient(uri, "/ext/index/C/block", requestTimeout),
		pindex:              indexer.NewClient(uri, "/ext/index/P/block", requestTimeout),
		ipcs:                ipcs.NewClient(uri, requestTimeout),
		keystore:            keystore.NewClient(uri, requestTimeout),
		platform:            platformvm.NewClient(uri, requestTimeout),
		cChain:              evm.NewCChainClient(uri, requestTimeout),
		byzantine:           newByzantineClient(byzURI, requestTimeout),
		cChainEth:           nil, // no point in dialing unless when needed
		cChainConcurrentEth: nil, // no point in dialing unless when needed
	}
}

func (c *Client) PIndexAPI() *indexer.Client {
	return c.pindex
}

func (c *Client) CIndexAPI() *indexer.Client {
	return c.cindex
}

func (c *Client) PChainAPI() *platformvm.Client {
	return c.platform
}

func (c *Client) XChainAPI() *avm.Client {
	return c.xChain
}

func (c *Client) XChainWalletAPI() *avm.WalletClient {
	return c.xChainWallet
}

func (c *Client) CChainAPI() *evm.Client {
	return c.cChain
}

func (c *Client) CChainEthAPI() *ethclient.Client {
	var err error
	var cClient *ethclient.Client
	if c.cChainEth == nil {
		for startTime := time.Now(); time.Since(startTime) < constants.TimeoutDuration; time.Sleep(time.Second) {
			cClient, err = ethclient.Dial(fmt.Sprintf("ws://%s:%d/ext/bc/C/ws", c.ipAddr, c.port))
			if err == nil {
				c.cChainEth = cClient
				c.cChainConcurrentEth = NewConcurrentEthClient(cClient)
				return c.cChainEth
			}
		}

		logrus.Infof("About to panic, the avalanchegoclient is unable to contact the CChain at : %s because of %v",
			fmt.Sprintf("ws://%s:%d/ext/bc/C/ws", c.ipAddr, c.port),
			err)
		panic(err)
	}

	return c.cChainEth
}

// CChainConcurrentEth wraps the ethclient.Client in a concurrency-safe implementation
func (c *Client) CChainConcurrentEth() *ConcurrentEthClient {
	if c.cChainEth == nil || c.cChainConcurrentEth.client == nil {
		c.cChainConcurrentEth = NewConcurrentEthClient(c.CChainEthAPI())
	}

	return c.cChainConcurrentEth
}

func (c *Client) InfoAPI() *info.Client {
	return c.info
}

func (c *Client) HealthAPI() *health.Client {
	return c.health
}

func (c *Client) IpcsAPI() *ipcs.Client {
	return c.ipcs
}

func (c *Client) KeystoreAPI() *keystore.Client {
	return c.keystore
}

func (c *Client) AdminAPI() *admin.Client {
	return c.admin
}

func (c *Client) ByzantineAPI() *ByzantineClient {
	return c.byzantine
}

// RedialEthClient forces a fresh connection on the CChainEthAPI and the CChainConcurrentEth libs
func (c *Client) RedialEthClient() *Client {
	c.cChainEth = nil
	c.cChainConcurrentEth = nil
	return c
}
