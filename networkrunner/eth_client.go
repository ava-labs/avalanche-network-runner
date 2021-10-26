package networkrunner

import (
	"context"
	"fmt"
	"math/big"
	"sync"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/coreth/core/types"
	"github.com/ava-labs/coreth/ethclient"
	"github.com/ava-labs/coreth/interfaces"
	"github.com/ethereum/go-ethereum/common"
)

// EthClient websocket ethclient.Client with lazy conn and mutexed api calls
type EthClient struct {
	ipAddr string
	port   uint
	client *ethclient.Client
	lock   sync.Mutex
}

func NewEthClient(ipAddr string, port uint) *EthClient {
	return &EthClient{
		ipAddr: ipAddr,
		port:   port,
	}
}

func (c *EthClient) connect() error {
	if c.client == nil {
		client, err := ethclient.Dial(fmt.Sprintf("ws://%s:%d/ext/bc/C/ws", c.ipAddr, c.port))
		if err != nil {
			return err
		}
		c.client = client
	}
	return nil
}

func (c *EthClient) Close() {
	if c.client == nil {
		return
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	c.client.Close()
}

func (c *EthClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	if err := c.connect(); err != nil {
		return err
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.SendTransaction(ctx, tx)
}

func (c *EthClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	if err := c.connect(); err != nil {
		return nil, err
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.TransactionReceipt(ctx, txHash)
}

func (c *EthClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	if err := c.connect(); err != nil {
		return nil, err
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.BalanceAt(ctx, account, blockNumber)
}

func (c *EthClient) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	if err := c.connect(); err != nil {
		return nil, err
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.BlockByNumber(ctx, number)
}

func (c *EthClient) BlockNumber(ctx context.Context) (uint64, error) {
	if err := c.connect(); err != nil {
		return 0, err
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.BlockNumber(ctx)
}

func (c *EthClient) CallContract(ctx context.Context, msg interfaces.CallMsg, blockNumber *big.Int) ([]byte, error) {
	if err := c.connect(); err != nil {
		return nil, err
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.CallContract(ctx, msg, blockNumber)
}

func (c *EthClient) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	if err := c.connect(); err != nil {
		return 0, err
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.NonceAt(ctx, account, blockNumber)
}

func (c *EthClient) AssetBalanceAt(ctx context.Context, account common.Address, assetID ids.ID, blockNumber *big.Int) (*big.Int, error) {
	if err := c.connect(); err != nil {
		return nil, err
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.AssetBalanceAt(ctx, account, assetID, blockNumber)
}
