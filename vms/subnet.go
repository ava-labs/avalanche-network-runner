package vms

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"

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

// most subfunctions share the same args...
type args struct {
	ctx            context.Context
	log            logging.Logger
	txPChainClient platformvm.Client
	fundedAddress  string
	userPass       api.UserPass
	allNodes       map[string]node.Node
	rSubnetID      ids.ID
}

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

	// initialize necessary args for the API calls
	args, err := initArgs(ctx, log, vm, network, privateKey)
	if err != nil {
		return fmt.Errorf("failed initializing subnet: %w", err)
	}

	// create the subnet
	if err := createSubnet(args); err != nil {
		return fmt.Errorf("failed creating subnet: %w", err)
	}

	// check the newly created subnet is in the subnet list
	if err := isSubnetInList(args); err != nil {
		return fmt.Errorf("failed to confirm subnet is in the node's subnet list")
	}

	// add all nodes as validators
	if err := addAllAsValidators(args, vm); err != nil {
		return fmt.Errorf("failed to add nodes as validators: %w", err)
	}

	// create the blockchain for this vm
	if err := createBlockchain(args, vm); err != nil {
		return fmt.Errorf("failed creating blockchain: %w", err)
	}

	// make sure all nodes are validating this new blockchain
	if err := finalizeBlockchain(args); err != nil {
		return fmt.Errorf("error checking all nodes are validating subnet: %w", err)
	}

	return nil
}

// initialize shared args
func initArgs(
	ctx context.Context,
	log logging.Logger,
	vm CustomVM,
	network network.Network,
	privateKey string,
) (*args, error) {
	userPass := defaultUserPass

	allNodes, err := network.GetAllNodes()
	if err != nil {
		return nil, err
	}

	txNodeNames, err := network.GetNodeNames()
	if err != nil {
		return nil, err
	}
	// txNode will be the node we issue all transactions on
	txNode := allNodes[txNodeNames[0]]
	txClient := txNode.GetAPIClient()
	// we need to create a user for platformvm calls
	ok, err := txClient.KeystoreAPI().CreateUser(userPass)
	if !ok || err != nil {
		return nil, fmt.Errorf("could not create user: %w", err)
	}

	txPChainClient := txClient.PChainAPI()
	// Import genesis key
	fundedAddress, err := txPChainClient.ImportKey(userPass, privateKey)
	if err != nil {
		return nil, fmt.Errorf("unable to import genesis key: %w", err)
	}
	balance, err := txPChainClient.GetBalance(fundedAddress)
	if err != nil {
		return nil, fmt.Errorf("unable to get genesis key balance: %w", err)
	}
	log.Info("found %d on address %s", balance.Balance, fundedAddress)

	rSubnetID, err := ids.FromString(vm.SubnetID)
	if err != nil {
		return nil, fmt.Errorf("invalid subnetID string: %w", err)
	}
	return &args{
		ctx:            ctx,
		log:            log,
		txPChainClient: txPChainClient,
		fundedAddress:  fundedAddress,
		userPass:       userPass,
		allNodes:       allNodes,
		rSubnetID:      rSubnetID,
	}, nil
}

func createSubnet(args *args) error {
	// Create a subnet
	subnetIDTx, err := args.txPChainClient.CreateSubnet(
		args.userPass,
		[]string{args.fundedAddress},
		args.fundedAddress,
		[]string{args.fundedAddress},
		defaultKeyThreshold,
	)
	if err != nil {
		return fmt.Errorf("unable to create subnet: %w", err)
	}

	for s := range args.allNodes {
		for {
			select {
			case <-args.ctx.Done():
				return args.ctx.Err()
			case <-time.After(loopTimeout):
			}
			status, _ := args.txPChainClient.GetTxStatus(subnetIDTx, true)
			if status.Status == platformvm.Committed {
				args.log.Info("subnet creation tx (%s) on (%s) accepted", subnetIDTx, s)
				break
			}
			args.log.Debug("waiting for subnet creation tx (%s) on (%s) to be accepted", subnetIDTx, s)
		}
	}
	args.log.Info("all nodex accepted subnet tx creation")
	return nil
}

func isSubnetInList(args *args) error {
	// confirm created subnet appears in subnet list
	_, err := args.txPChainClient.GetSubnets([]ids.ID{args.rSubnetID})
	if err != nil {
		return fmt.Errorf("subnet not found: %w", err)
	}
	return nil
}

func addAllAsValidators(args *args, vm CustomVM) error {
	// Add all validators to subnet with equal weight
	for _, n := range args.allNodes {
		nodeID := n.GetNodeID().PrefixedString(constants.NodeIDPrefix)
		txID, err := args.txPChainClient.AddSubnetValidator(
			args.userPass, []string{args.fundedAddress}, args.fundedAddress,
			vm.SubnetID, nodeID, validatorWeight,
			uint64(time.Now().Add(validatorStartDiff).Unix()),
			uint64(time.Now().Add(validatorEndDiff).Unix()),
		)
		if err != nil {
			return fmt.Errorf("unable to add subnet validator: %w", err)
		}

		for {
			select {
			case <-args.ctx.Done():
				return args.ctx.Err()
			case <-time.After(loopTimeout):
			}
			status, _ := args.txPChainClient.GetTxStatus(txID, true)
			if status.Status == platformvm.Committed {
				args.log.Info("add subnet validator (%s) tx (%s) accepted", nodeID, txID)
				break
			}
			args.log.Debug("waiting for add subnet validator (%s) tx (%s) to be accepted", nodeID, txID)
		}
	}
	return nil
}

func createBlockchain(args *args, vm CustomVM) error {
	// Create blockchain
	genesis, err := ioutil.ReadFile(vm.Genesis)
	if err != nil {
		return fmt.Errorf("could not read genesis file (%s): %w", vm.Genesis, err)
	}
	txID, err := args.txPChainClient.CreateBlockchain(
		args.userPass, []string{args.fundedAddress}, args.fundedAddress, args.rSubnetID,
		vm.ID, []string{}, vm.Name, genesis,
	)
	if err != nil {
		return fmt.Errorf("could not create blockchain: %w", err)
	}
	for {
		select {
		case <-args.ctx.Done():
			return args.ctx.Err()
		case <-time.After(loopTimeout):
		}
		status, _ := args.txPChainClient.GetTxStatus(txID, true)
		if status.Status == platformvm.Committed {
			args.log.Info("create blockchain tx (%s) accepted", txID)
			return nil
		}
		args.log.Debug("waiting for create blockchain tx (%s) to be accepted", txID)
	}
}

func finalizeBlockchain(args *args) error {
	// Validate blockchain exists
	blockchains, err := args.txPChainClient.GetBlockchains()
	if err != nil {
		return fmt.Errorf("could not query blockchains: %w", err)
	}
	var blockchainID ids.ID
	for _, blockchain := range blockchains {
		if blockchain.SubnetID == args.rSubnetID {
			blockchainID = blockchain.ID
			break
		}
	}
	if blockchainID == (ids.ID{}) {
		return errors.New("could not find blockchain")
	}

	if err := ensureValidating(args, blockchainID); err != nil {
		return fmt.Errorf("error checking all nodes are validating the blockchain: %w", err)
	}
	if err := ensureBootstrapped(args, blockchainID); err != nil {
		return fmt.Errorf("error checking blockchain is bootstrapped: %w", err)
	}
	// Print endpoints where VM is accessible
	args.log.Info("Custom VM endpoints now accessible at:")
	for _, n := range args.allNodes {
		args.log.Info("%s: %s:%d/ext/bc/%s", n.GetNodeID(), n.GetURL(), n.GetAPIPort(), blockchainID.String())
	}
	return nil
}

func ensureValidating(args *args, blockchainID ids.ID) error {
	statusCheckTimeout := longTimeout
	// Ensure all nodes are validating subnet
	for _, n := range args.allNodes {
		nodeID := n.GetNodeID().PrefixedString(constants.NodeIDPrefix)
		nClient := n.GetAPIClient().PChainAPI()
		for {
			select {
			case <-args.ctx.Done():
				return args.ctx.Err()
			case <-time.After(statusCheckTimeout):
			}
			status, err := nClient.GetBlockchainStatus(blockchainID.String())
			if err != nil {
				return fmt.Errorf("error querying blockchain status: %w", err)
			}
			if status == platformvm.Validating {
				// after the first acceptance, next nodes probably don't need to check that long anymore
				statusCheckTimeout = loopTimeout
				break
			}
			args.log.Debug("waiting for validating status for %s", nodeID)
		}
		args.log.Info("%s validating blockchain %s", nodeID, blockchainID)
	}

	return nil
}

func ensureBootstrapped(args *args, blockchainID ids.ID) error {
	// Ensure network bootstrapped
	for _, n := range args.allNodes {
		nodeID := n.GetNodeID().PrefixedString(constants.NodeIDPrefix)
		nClient := n.GetAPIClient().InfoAPI()
		for {
			select {
			case <-args.ctx.Done():
				return args.ctx.Err()
			case <-time.After(loopTimeout):
			}
			bootstrapped, _ := nClient.IsBootstrapped(blockchainID.String())
			if bootstrapped {
				break
			}
			args.log.Debug("waiting for %s to bootstrap %s", nodeID, blockchainID.String())
		}
		args.log.Info("%s bootstrapped %s", nodeID, blockchainID)
	}
	return nil
}
