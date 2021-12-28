package vms

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network"

	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/api/keystore"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/fatih/color"
)

type CustomVM struct {
	Path     string
	Genesis  string
	Name     string
	SubnetID string
	ID       string
}

const (
	// TODO this also can't be hardcoded
	genesisKey   = "PrivateKey-ewoqjP7PxY4yr3iLTpLisriqt94hdyDFNgchSxGGztUrTXtNN"
	waitTime     = 1 * time.Second
	longWaitTime = 10 * waitTime
	protocol     = "http://"

	validatorWeight    = 3000
	validatorStartDiff = 30 * time.Second
	validatorEndDiff   = 30 * 24 * time.Hour // 30 days
	HTTPTimeout        = 10 * time.Second
)

func SetupSubnet(
	ctx context.Context,
	vm CustomVM,
	network network.Network,
) error {
	color.Cyan("creating subnet")

	userPass := api.UserPass{
		Username: "test",
		Password: "vmsrkewl",
	}

	allNodes, err := network.GetAllNodes()
	if err != nil {
		return err
	}
	nodeURLs := make([]string, len(allNodes))
	nodeIDs := make([]string, len(allNodes))
	i := 0
	for _, n := range allNodes {
		nodeURLs[i] = fmt.Sprintf("%s%s:%d", protocol, n.GetURL(), n.GetAPIPort())
		nodeIDs[i] = "NodeID-" + n.GetNodeID().String()
		i++
	}
	// Create user
	kclient := keystore.NewClient(nodeURLs[0], HTTPTimeout)
	ok, err := kclient.CreateUser(userPass)
	if !ok || err != nil {
		return fmt.Errorf("could not create user: %w", err)
	}

	// Connect to local network
	client := platformvm.NewClient(nodeURLs[0], HTTPTimeout)

	// Import genesis key
	fundedAddress, err := client.ImportKey(userPass, genesisKey)
	if err != nil {
		return fmt.Errorf("unable to import genesis key: %w", err)
	}
	balance, err := client.GetBalance(fundedAddress)
	if err != nil {
		return fmt.Errorf("unable to get genesis key balance: %w", err)
	}
	color.Cyan("found %d on address %s", balance, fundedAddress)

	// Create a subnet
	subnetIDTx, err := client.CreateSubnet(
		userPass,
		[]string{fundedAddress},
		fundedAddress,
		[]string{fundedAddress},
		1,
	)
	if err != nil {
		return fmt.Errorf("unable to create subnet: %w", err)
	}

CREATE_SUBNET:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			status, _ := client.GetTxStatus(subnetIDTx, true)
			if status.Status == platformvm.Committed {
				break CREATE_SUBNET
			}
			color.Yellow("waiting for subnet creation tx (%s) to be accepted", subnetIDTx)
		}
	}
	color.Cyan("subnet creation tx (%s) accepted", subnetIDTx)

	// Confirm created subnet appears in subnet list
	subnets, err := client.GetSubnets([]ids.ID{})
	if err != nil {
		return fmt.Errorf("cannot query subnets: %w", err)
	}

	var rSubnetID ids.ID
	var subnetID string
	for _, s := range subnets {
		if s.ID.String() == vm.SubnetID {
			rSubnetID = s.ID
			subnetID = s.ID.String()
			break
		}
	}
	if subnetID != vm.SubnetID {
		return fmt.Errorf("expected subnet %s but got %s", vm.SubnetID, subnetID)
	}

	// Add all validators to subnet with equal weight
	for _, nodeID := range nodeIDs {
		txID, err := client.AddSubnetValidator(
			userPass, []string{fundedAddress}, fundedAddress,
			subnetID, nodeID, validatorWeight,
			uint64(time.Now().Add(validatorStartDiff).Unix()),
			uint64(time.Now().Add(validatorEndDiff).Unix()),
		)
		if err != nil {
			return fmt.Errorf("unable to add subnet validator: %w", err)
		}

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waitTime):
				status, _ := client.GetTxStatus(txID, true)
				if status.Status == platformvm.Committed {
					break
				}
				color.Yellow("waiting for add subnet validator (%s) tx (%s) to be accepted", nodeID, txID)
			}
			color.Cyan("add subnet validator (%s) tx (%s) accepted", nodeID, txID)
		}
	}

	// Create blockchain
	genesis, err := ioutil.ReadFile(vm.Genesis)
	if err != nil {
		return fmt.Errorf("could not read genesis file (%s): %w", vm.Genesis, err)
	}
	txID, err := client.CreateBlockchain(
		userPass, []string{fundedAddress}, fundedAddress, rSubnetID,
		vm.ID, []string{}, vm.Name, genesis,
	)
	if err != nil {
		return fmt.Errorf("could not create blockchain: %w", err)
	}
STATUS:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			status, _ := client.GetTxStatus(txID, true)
			if status.Status == platformvm.Committed {
				break STATUS
			}
			color.Yellow("waiting for create blockchain tx (%s) to be accepted", txID)
		}
	}
	color.Cyan("create blockchain tx (%s) accepted", txID)

	// Validate blockchain exists
	blockchains, err := client.GetBlockchains()
	if err != nil {
		return fmt.Errorf("could not query blockchains: %w", err)
	}
	var blockchainID ids.ID
	for _, blockchain := range blockchains {
		if blockchain.SubnetID == rSubnetID {
			blockchainID = blockchain.ID
			break
		}
	}
	if blockchainID == (ids.ID{}) {
		return errors.New("could not find blockchain")
	}

	// Ensure all nodes are validating subnet
	for i, url := range nodeURLs {
		nClient := platformvm.NewClient(url, HTTPTimeout)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(longWaitTime):
				status, err := nClient.GetBlockchainStatus(blockchainID.String())
				if err != nil {
					return fmt.Errorf("error querying blockchain status: %w", err)
				}
				if status == platformvm.Validating {
					break
				}
				color.Yellow("waiting for validating status for %s", nodeIDs[i])
			}
		}
	}
	color.Cyan("%s validating blockchain %s", nodeIDs[i], blockchainID)

	// Ensure network bootstrapped
	for i, url := range nodeURLs {
		nClient := info.NewClient(url, HTTPTimeout)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waitTime):
				bootstrapped, _ := nClient.IsBootstrapped(blockchainID.String())
				if bootstrapped {
					break
				}
				color.Yellow("waiting for %s to bootstrap %s", nodeIDs[i], blockchainID.String())
			}
		}
	}
	color.Cyan("%s bootstrapped %s", nodeIDs[i], blockchainID)

	// Print endpoints where VM is accessible
	color.Green("Custom VM endpoints now accessible at:")
	for i, url := range nodeURLs {
		color.Green("%s: %s/ext/bc/%s", nodeIDs[i], url, blockchainID.String())
	}
	return nil
}
