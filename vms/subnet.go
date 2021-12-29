package vms

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network"

	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms/platformvm"
)

// CustomVM wraps data to create a custom VM
type CustomVM struct {
	Path     string
	Genesis  string
	Name     string
	SubnetID string
	ID       string
}

const (
	loopTimeout         = 1 * time.Second
	longTimeout         = 10 * loopTimeout
	defaultKeyThreshold = 1

	validatorWeight    = 3000
	validatorStartDiff = 30 * time.Second
	validatorEndDiff   = 30 * 24 * time.Hour // 30 days
)

// SetupSubnet creates the necessary transactions to create a subnet for a given custom VM.
// It then also waits until all these transactions are confirmed on all nodes.
// Finally it creates a blockchain and makes sure all nodes of the given network
// are validating this blockchain.
// It requires a `privateKey` in order to issue the necessary transactions
func SetupSubnet(
	ctx context.Context,
	log logging.Logger,
	vm CustomVM,
	network network.Network,
	privateKey string,
) error {
	log.Info("creating subnet")

	userPass := api.UserPass{
		Username: "test",
		Password: "vmsrkewl",
	}

	allNodes, err := network.GetAllNodes()
	if err != nil {
		return err
	}

	txNodeNames, err := network.GetNodeNames()
	if err != nil {
		return err
	}
	// txNode will be the node we issue all transactions on
	txNode := allNodes[txNodeNames[0]]
	txClient := txNode.GetAPIClient()
	// we need to create a user for platformvm calls
	ok, err := txClient.KeystoreAPI().CreateUser(userPass)
	if !ok || err != nil {
		return fmt.Errorf("could not create user: %w", err)
	}

	txPChainClient := txClient.PChainAPI()
	// Import genesis key
	fundedAddress, err := txPChainClient.ImportKey(userPass, privateKey)
	if err != nil {
		return fmt.Errorf("unable to import genesis key: %w", err)
	}
	balance, err := txPChainClient.GetBalance(fundedAddress)
	if err != nil {
		return fmt.Errorf("unable to get genesis key balance: %w", err)
	}
	log.Info("found %d on address %s", balance.Balance, fundedAddress)

	// Create a subnet
	subnetIDTx, err := txPChainClient.CreateSubnet(
		userPass,
		[]string{fundedAddress},
		fundedAddress,
		[]string{fundedAddress},
		defaultKeyThreshold,
	)
	if err != nil {
		return fmt.Errorf("unable to create subnet: %w", err)
	}

CREATE_SUBNET:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(loopTimeout):
			status, _ := txPChainClient.GetTxStatus(subnetIDTx, true)
			if status.Status == platformvm.Committed {
				break CREATE_SUBNET
			}
			log.Debug("waiting for subnet creation tx (%s) to be accepted", subnetIDTx)
		}
	}
	log.Info("subnet creation tx (%s) accepted", subnetIDTx)

	// Confirm created subnet appears in subnet list
	subnets, err := txPChainClient.GetSubnets([]ids.ID{})
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
	for _, n := range allNodes {
		nodeID := n.GetNodeID().PrefixedString(constants.NodeIDPrefix)
		txID, err := txPChainClient.AddSubnetValidator(
			userPass, []string{fundedAddress}, fundedAddress,
			subnetID, nodeID, validatorWeight,
			uint64(time.Now().Add(validatorStartDiff).Unix()),
			uint64(time.Now().Add(validatorEndDiff).Unix()),
		)
		if err != nil {
			return fmt.Errorf("unable to add subnet validator: %w", err)
		}

	ADD_VALIDATOR:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(loopTimeout):
				status, _ := txPChainClient.GetTxStatus(txID, true)
				if status.Status == platformvm.Committed {
					break ADD_VALIDATOR
				}
				log.Debug("waiting for add subnet validator (%s) tx (%s) to be accepted", nodeID, txID)
			}
			log.Info("add subnet validator (%s) tx (%s) accepted", nodeID, txID)
		}
	}

	// Create blockchain
	genesis, err := ioutil.ReadFile(vm.Genesis)
	if err != nil {
		return fmt.Errorf("could not read genesis file (%s): %w", vm.Genesis, err)
	}
	txID, err := txPChainClient.CreateBlockchain(
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
		case <-time.After(loopTimeout):
			status, _ := txPChainClient.GetTxStatus(txID, true)
			if status.Status == platformvm.Committed {
				break STATUS
			}
			log.Debug("waiting for create blockchain tx (%s) to be accepted", txID)
		}
	}
	log.Info("create blockchain tx (%s) accepted", txID)

	// Validate blockchain exists
	blockchains, err := txPChainClient.GetBlockchains()
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

	statusCheckTimeout := longTimeout
	// Ensure all nodes are validating subnet
	for _, n := range allNodes {
		nodeID := n.GetNodeID().PrefixedString(constants.NodeIDPrefix)
		nClient := n.GetAPIClient().PChainAPI()
	VALIDATING:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(statusCheckTimeout):
				status, err := nClient.GetBlockchainStatus(blockchainID.String())
				if err != nil {
					return fmt.Errorf("error querying blockchain status: %w", err)
				}
				if status == platformvm.Validating {
					// after the first acceptance, next nodes probably don't need to check that long anymore
					statusCheckTimeout = loopTimeout
					break VALIDATING
				}
				log.Debug("waiting for validating status for %s", nodeID)
			}
		}
		log.Info("%s validating blockchain %s", nodeID, blockchainID)
	}

	// Ensure network bootstrapped
	for _, n := range allNodes {
		nodeID := n.GetNodeID().PrefixedString(constants.NodeIDPrefix)
		nClient := n.GetAPIClient().InfoAPI()
	BOOTSTRAPPED:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(loopTimeout):
				bootstrapped, _ := nClient.IsBootstrapped(blockchainID.String())
				if bootstrapped {
					break BOOTSTRAPPED
				}
				log.Debug("waiting for %s to bootstrap %s", nodeID, blockchainID.String())
			}
		}
		log.Info("%s bootstrapped %s", nodeID, blockchainID)
	}

	// Print endpoints where VM is accessible
	log.Info("Custom VM endpoints now accessible at:")
	for _, n := range allNodes {
		log.Info("%s: %s:%d/ext/bc/%s", n.GetNodeID(), n.GetURL(), n.GetAPIPort(), blockchainID.String())
	}
	return nil
}
