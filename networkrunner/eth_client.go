package networkrunner

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/coreth/interfaces"
	"github.com/ava-labs/coreth/core/types"
	"github.com/ava-labs/coreth/ethclient"
	"github.com/ethereum/go-ethereum/common"
	"github.com/sirupsen/logrus"
)

// EthClient is a concurrency-safe implementation
// of ethclient.Client that allows for multiple concurrent
// requests to be made to a single *services.Client.
type EthClient struct {
    ipAddr string
    port uint
	client *ethclient.Client
	lock   sync.Mutex
}

func NewEthClient(ipAddr string, port uint) *EthClient {
	return &EthClient{
        ipAddr: ipAddr,
        port: port,
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

// Close terminates the client's connection.
func (c *EthClient) Close() {
    if c.client == nil {
        return
    }
	c.lock.Lock()
	defer c.lock.Unlock()
	c.client.Close()
}

// SendTransaction injects a signed transaction into the pending pool for execution.
//
// If the transaction was a contract creation use the TransactionReceipt method to get the
// contract address after the transaction has been mined.
func (c *EthClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
    if err := c.connect(); err != nil {
        return err
    }
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.SendTransaction(ctx, tx)
}

// TransactionReceipt returns the receipt of a transaction by transaction hash.
// Note that the receipt is not available for pending transactions.
func (c *EthClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
    if err := c.connect(); err != nil {
        return nil, err
    }
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.TransactionReceipt(ctx, txHash)
}

// BalanceAt returns the wei balance of the given account.
// The block number can be nil, in which case the balance is taken from the latest known block.
func (c *EthClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
    if err := c.connect(); err != nil {
        return nil, err
    }
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.BalanceAt(ctx, account, blockNumber)
}

// BlockByNumber returns a block from the current canonical chain. If number is nil, the
// latest known block is returned.
//
// Note that loading full blocks requires two requests. Use HeaderByNumber
// if you don't need all transactions or uncle headers.
func (c *EthClient) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
    if err := c.connect(); err != nil {
        return nil, err
    }
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.client.BlockByNumber(ctx, number)
}

// BlockNumber returns the most recent block number
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

// ForceSendTransaction attempts to submit a transaction until it succeeds or
// until a non-transient error is returned.
func (c *EthClient) ForceSendTransaction(ctx context.Context, tx *types.Transaction) error {
    if err := c.connect(); err != nil {
        return err
    }
	maxSendRetries := 10
	sendRetrySleep := 5 * time.Second
	for i := 0; i < maxSendRetries; i++ {
		err := c.SendTransaction(ctx, tx)
		if err == nil {
			return nil
		}

		if errors.Is(err, context.Canceled) {
			return ctx.Err()
		}

		// errors.Is does not catch these for some reason
		if strings.Contains(err.Error(), "already known") || strings.Contains(err.Error(), "nonce too low") {
			logrus.Warnf("Not resubmitting %s, received error: %s", tx.Hash().Hex(), err.Error())
			return nil
		}

		logrus.Warnf("Received transient error (%s), forcing send %s (retry %d)", err.Error(), tx.Hash().Hex(), i)
		time.Sleep(sendRetrySleep)
	}

	return fmt.Errorf("could not force send of %s", tx.Hash().Hex())
}

