package api

import (
	"context"
	"fmt"
	"math/big"
	"sync"

	"github.com/ava-labs/coreth/core/types"
	"github.com/ava-labs/coreth/ethclient"
	"github.com/ava-labs/coreth/interfaces"
	"github.com/ethereum/go-ethereum/common"
)

// Interface compliance
var _ EthClient = &ethClient{}

type EthClient interface {
	Close()
	SendTransaction(context.Context, *types.Transaction) error
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
	BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error)
	BlockByNumber(context.Context, *big.Int) (*types.Block, error)
	BlockByHash(context.Context, common.Hash) (*types.Block, error)
	BlockNumber(context.Context) (uint64, error)
	CallContract(context.Context, interfaces.CallMsg, *big.Int) ([]byte, error)
	NonceAt(context.Context, common.Address, *big.Int) (uint64, error)
	SuggestGasPrice(context.Context) (*big.Int, error)
	AcceptedCodeAt(context.Context, common.Address) ([]byte, error)
	AcceptedNonceAt(context.Context, common.Address) (uint64, error)
	CodeAt(context.Context, common.Address, *big.Int) ([]byte, error)
	EstimateGas(context.Context, interfaces.CallMsg) (uint64, error)
	AcceptedCallContract(context.Context, interfaces.CallMsg) ([]byte, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
	SuggestGasTipCap(context.Context) (*big.Int, error)
	FilterLogs(context.Context, interfaces.FilterQuery) ([]types.Log, error)
	SubscribeFilterLogs(context.Context, interfaces.FilterQuery, chan<- types.Log) (interfaces.Subscription, error)
}

// ethClient websocket ethclient.Client with mutexed api calls and lazy conn (on first call)
// All calls are wrapped in a mutex, and try to create a connection if it doesn't exist yet
type ethClient struct {
	ipAddr  string
	chainID string
	port    uint
	client  ethclient.Client
	lock    sync.Mutex
}

// NewEthClient mainly takes ip/port info for usage in future calls
// Connection can't be initialized in constructor because node is not ready when the constructor is called
// It follows convention of most avalanchego api constructors that can be called without having a ready node
func NewEthClient(ipAddr string, port uint) EthClient {
	// default to using the C chain
	return NewEthClientWithChainID(ipAddr, port, "C")
}

// NewEthClientWithChainID creates an EthClient initialized to connect to
// ipAddr/port and communicate with the given chainID.
func NewEthClientWithChainID(ipAddr string, port uint, chainID string) EthClient {
	return &ethClient{
		ipAddr:  ipAddr,
		port:    port,
		chainID: chainID,
	}
}

// connect attempts to connect with websocket ethclient API
func (c *ethClient) connect() error {
	if c.client == ethclient.Client(nil) {
		client, err := ethclient.Dial(fmt.Sprintf("ws://%s:%d/ext/bc/%s/ws", c.ipAddr, c.port, c.chainID))
		if err != nil {
			return err
		}
		c.client = client
	}
	return nil
}

// Close closes opened connection (if any)
func (c *ethClient) Close() {
	if c.client == ethclient.Client(nil) {
		return
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	c.client.Close()
}

func (c *ethClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return err
	}
	return c.client.SendTransaction(ctx, tx)
}

func (c *ethClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.TransactionReceipt(ctx, txHash)
}

func (c *ethClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.BalanceAt(ctx, account, blockNumber)
}

func (c *ethClient) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.BlockByNumber(ctx, number)
}

func (c *ethClient) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.BlockByHash(ctx, hash)
}

func (c *ethClient) BlockNumber(ctx context.Context) (uint64, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return 0, err
	}
	return c.client.BlockNumber(ctx)
}

func (c *ethClient) CallContract(ctx context.Context, msg interfaces.CallMsg, blockNumber *big.Int) ([]byte, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.CallContract(ctx, msg, blockNumber)
}

func (c *ethClient) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return 0, err
	}
	return c.client.NonceAt(ctx, account, blockNumber)
}

func (c *ethClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.SuggestGasPrice(ctx)
}

func (c *ethClient) AcceptedCodeAt(ctx context.Context, account common.Address) ([]byte, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.AcceptedCodeAt(ctx, account)
}

func (c *ethClient) AcceptedNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return 0, err
	}
	return c.client.AcceptedNonceAt(ctx, account)
}

func (c *ethClient) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.CodeAt(ctx, account, blockNumber)
}

func (c *ethClient) EstimateGas(ctx context.Context, msg interfaces.CallMsg) (uint64, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return 0, err
	}
	return c.client.EstimateGas(ctx, msg)
}

func (c *ethClient) AcceptedCallContract(ctx context.Context, call interfaces.CallMsg) ([]byte, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.AcceptedCallContract(ctx, call)
}

func (c *ethClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.HeaderByNumber(ctx, number)
}

func (c *ethClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.SuggestGasTipCap(ctx)
}

func (c *ethClient) FilterLogs(ctx context.Context, query interfaces.FilterQuery) ([]types.Log, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.FilterLogs(ctx, query)
}

func (c *ethClient) SubscribeFilterLogs(ctx context.Context, query interfaces.FilterQuery, ch chan<- types.Log) (interfaces.Subscription, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c.client.SubscribeFilterLogs(ctx, query, ch)
}
