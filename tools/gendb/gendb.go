package gendb

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/coreth/core/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const pk = "PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN"

var userPass = api.UserPass{
	Username: "test",
	Password: "+Do_Not-UseTh1s",
}

type GenDBConfig struct {
	NumTxs           int
	IncludeX         bool
	IncludeP         bool
	IncludeC         bool
	IncludeContracts bool
}

func GenDB(net network.Network, conf GenDBConfig) error {
	fmt.Println("creating key stores...")
	if err := createKeyStores(net); err != nil {
		return err
	}
	fmt.Println("creating X txs...")
	if conf.IncludeX {
		// go createXTxs(net, conf.NumTxs)
		err := createXTxs(net, conf.NumTxs)
		if err != nil {
			return err
		}
	}
	fmt.Println("creating P txs...")
	if conf.IncludeP {
		//	go createPTxs(net, conf.NumTxs)
		err := createPTxs(net, conf.NumTxs)
		if err != nil {
			return err
		}
	}
	fmt.Println("creating C txs...")
	if conf.IncludeC {
		// go createCTxs(net, conf.NumTxs)
		err := createCTxs(net, conf.NumTxs)
		if err != nil {
			return err
		}
	}
	if conf.IncludeContracts {
		go deployContracts(net)
	}
	return nil
}

func createXTxs(net network.Network, numTxs int) error {
	sender, err := net.GetNode(net.GetNodesNames()[0])
	if err != nil {
		return err
	}
	from := []string{"X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p"}
	to := "X-custom1ur873jhz9qnaqv5qthk5sn3e8nj3e0kmzpjrhp"
	for i := 0; i < numTxs; i++ {
		// go sender.GetAPIClient().XChainWalletAPI().Send(userPass, from, from[0], 10, "AVAX", to, "bla")
		sender.GetAPIClient().XChainWalletAPI().Send(userPass, from, from[0], 10, "AVAX", to, "bla")
	}
	return nil
}

func createPTxs(net network.Network, numTxs int) error {
	nodes := net.GetNodesNames()

	node, _ := net.GetNode(nodes[0])
	for i := 0; i < numTxs; i++ {
		from := []string{"P-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p"}
		_, err := node.GetAPIClient().PChainAPI().CreateSubnet(userPass, from, from[0], from, 0)
		if err != nil {
			return err
		}
	}
	return nil
}

func createCTxs(net network.Network, numTxs int) error {
	signer := types.NewEIP2930Signer(big.NewInt(int64(43112)))
	node, err := net.GetNode(net.GetNodesNames()[0])
	if err != nil {
		return err
	}
	_, err = node.GetAPIClient().CChainAPI().ImportKey(userPass, pk)
	if err != nil {
		return err
	}

	pks := "56289e99c94b6912bfc12adc093c9b51124f0dc54ac7a766b2bc5ccf558d8027"
	pk, err := crypto.HexToECDSA(pks)
	if err != nil {
		return err
	}

	recvpk, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	recvaddr := crypto.PubkeyToAddress(recvpk.PublicKey)
	for i := 0; i < numTxs; i++ {
		nonce := uint64(i)
		var tx *types.Transaction
		if i%2 == 0 { //nolint:gosec // Don't need cryptographically secure randomness
			tx = types.NewTx(&types.LegacyTx{
				Nonce:    nonce,
				To:       &recvaddr,
				Value:    big.NewInt(1),
				Gas:      21000,
				GasPrice: big.NewInt(225_000_000_000),
			})
		} else {
			tx = types.NewTx(&types.AccessListTx{
				Nonce:      nonce,
				To:         &recvaddr,
				Value:      big.NewInt(1),
				Gas:        50_000,
				GasPrice:   big.NewInt(225_000_000_000),
				AccessList: types.AccessList{{Address: recvaddr, StorageKeys: []common.Hash{{0}}}},
			})
		}
		signedTx, err := types.SignTx(tx, signer, pk)
		if err != nil {
			return fmt.Errorf("failed to sign transaction: %w", err)
		}

		go node.GetAPIClient().CChainEthAPI().SendTransaction(context.Background(), signedTx)

	}
	return nil
}

func deployContracts(net network.Network) {
}

func createKeyStores(net network.Network) error {
	nodes := net.GetNodesNames()

	for _, n := range nodes {
		node, err := net.GetNode(n)
		if err != nil {
			return err
		}
		_, err = node.GetAPIClient().KeystoreAPI().CreateUser(userPass)
		if err != nil {
			fmt.Println("err creating key store")
			return err
		}
		_, err = node.GetAPIClient().XChainAPI().ImportKey(userPass, pk)
		if err != nil {
			fmt.Println("err X importing key")
			return err
		}
		_, err = node.GetAPIClient().PChainAPI().ImportKey(userPass, pk)
		if err != nil {
			fmt.Println("err P importing key")
			return err
		}
	}
	return nil
}
