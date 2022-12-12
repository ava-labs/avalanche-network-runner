// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/api/admin"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/platformvm/validator"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary/common"
	"go.uber.org/zap"
)

const (
	// offset of validation start from current time
	validationStartOffset = 20 * time.Second
	// duration for primary network validators
	validationDuration = 365 * 24 * time.Hour
	// weight assigned to subnet validators
	subnetValidatorsWeight = 1000
	// check period for blockchain logs while waiting for custom chains to be ready
	blockchainLogPullFrequency = time.Second
	// check period while waiting for all validators to be ready
	waitForValidatorsPullFrequency = time.Second
	defaultTimeout                 = time.Minute
)

var (
	errAborted  = errors.New("aborted")
	defaultPoll = common.WithPollFrequency(100 * time.Millisecond)
)

type blockchainInfo struct {
	chainName    string
	vmID         ids.ID
	subnetID     ids.ID
	blockchainID ids.ID
}

// get an arbitrary node in the network
func (ln *localNetwork) getSomeNode() node.Node {
	var node node.Node
	for _, n := range ln.nodes {
		node = n
		break
	}
	return node
}

// get node client URI for an arbitrary node in the network
func (ln *localNetwork) getClientURI() (string, error) {
	node := ln.getSomeNode()
	clientURI := fmt.Sprintf("http://%s:%d", node.GetURL(), node.GetAPIPort())
	return clientURI, nil
}

func (ln *localNetwork) CreateBlockchains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec, // VM name + genesis bytes
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()
	chainInfos, err := ln.installCustomChains(ctx, chainSpecs)
	if err != nil {
		return err
	}

	if err := ln.waitForCustomChainsReady(ctx, chainInfos); err != nil {
		return err
	}
	return nil
}

func (ln *localNetwork) CreateSubnets(
	ctx context.Context,
	numSubnets uint32,
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()
	if _, err := ln.setupWalletAndInstallSubnets(ctx, numSubnets); err != nil {
		return err
	}
	return nil
}

// provisions local cluster and install custom chains if applicable
// assumes the local cluster is already set up and healthy
func (ln *localNetwork) installCustomChains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
) ([]blockchainInfo, error) {
	fmt.Println()
	ln.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("create and install custom chains")))

	clientURI, err := ln.getClientURI()
	if err != nil {
		return nil, err
	}
	platformCli := platformvm.NewClient(clientURI)

	// wallet needs txs for all previously created subnets
	var pTXs []ids.ID
	for _, chainSpec := range chainSpecs {
		// if subnet id for the blockchain is specified, we need to add the subnet id
		// tx info to the wallet so blockchain creation does not fail
		// if subnet id is not specified, a new subnet will later be created by using the wallet,
		// and the wallet will obtain the tx info at that moment
		if chainSpec.SubnetId != nil {
			subnetID, err := ids.FromString(*chainSpec.SubnetId)
			if err != nil {
				return nil, err
			}
			pTXs = append(pTXs, subnetID)
		}
	}

	baseWallet, avaxAssetID, testKeyAddr, err := setupWallet(ctx, clientURI, pTXs, ln.log)
	if err != nil {
		return nil, err
	}

	if err := ln.addPrimaryValidators(ctx, platformCli, baseWallet, testKeyAddr); err != nil {
		return nil, err
	}

	// get number of subnets to create
	// for the list of requested blockchains, we count those that have undefined subnet id
	// that number of subnets will be created and later assigned to those blockchain requests
	var numSubnetsToCreate uint32
	for _, chainSpec := range chainSpecs {
		if chainSpec.SubnetId == nil {
			numSubnetsToCreate++
		}
	}
	// create missing subnets
	addedSubnetIDs, err := createSubnets(ctx, numSubnetsToCreate, baseWallet, testKeyAddr, ln.log)
	if err != nil {
		return nil, err
	}
	// assign created subnets to blockchain requests with undefined subnet id
	j := 0
	for i := range chainSpecs {
		if chainSpecs[i].SubnetId == nil {
			subnetIDStr := addedSubnetIDs[j].String()
			chainSpecs[i].SubnetId = &subnetIDStr
			j++
		}
	}

	blockchainTxs, err := createBlockchainTxs(ctx, chainSpecs, baseWallet, ln.log)
	if err != nil {
		return nil, err
	}
	blockchainFilesCreated, err := ln.createBlockchainConfigFiles(chainSpecs, blockchainTxs, ln.log)
	if err != nil {
		return nil, err
	}

	if numSubnetsToCreate > 0 || blockchainFilesCreated {
		// we need to restart if there are new subnets or if there are new network config files
		// add missing subnets, restarting network and waiting for subnet validation to start
		baseWallet, err = ln.restartNodesAndResetWallet(ctx, addedSubnetIDs, pTXs, clientURI)
		if err != nil {
			return nil, err
		}
	}

	// refresh vm list
	if err := ln.reloadVMPlugins(ctx); err != nil {
		return nil, err
	}

	// create blockchain from txs before spending more utxos
	if err := ln.createBlockchains(ctx, chainSpecs, blockchainTxs, baseWallet, ln.log); err != nil {
		return nil, err
	}

	// get all subnets for add subnet validator request
	subnetIDs := []ids.ID{}
	for _, chainSpec := range chainSpecs {
		subnetID, err := ids.FromString(*chainSpec.SubnetId)
		if err != nil {
			return nil, err
		}
		subnetIDs = append(subnetIDs, subnetID)
	}
	// add subnet validators
	if err = ln.addSubnetValidators(ctx, platformCli, baseWallet, subnetIDs); err != nil {
		return nil, err
	}

	chainInfos := make([]blockchainInfo, len(chainSpecs))
	for i, chainSpec := range chainSpecs {
		vmID, err := utils.VMID(chainSpec.VmName)
		if err != nil {
			return nil, err
		}
		subnetID, err := ids.FromString(*chainSpec.SubnetId)
		if err != nil {
			return nil, err
		}
		chainInfos[i] = blockchainInfo{
			// we keep a record of VM name in blockchain name field,
			// as there is no way to recover VM name from VM ID
			chainName:    chainSpec.VmName,
			vmID:         vmID,
			subnetID:     subnetID,
			blockchainID: blockchainTxs[i].ID(),
		}
	}

	fmt.Println()
	ln.log.Info(logging.Green.Wrap("checking the remaining balance of the base wallet"))
	balances, err := baseWallet.P().Builder().GetBalance()
	if err != nil {
		return nil, err
	}
	ln.log.Info("base wallet AVAX balance", zap.Uint64("balance", balances[avaxAssetID]), zap.String("address", testKeyAddr.String()))

	return chainInfos, nil
}

func (ln *localNetwork) setupWalletAndInstallSubnets(
	ctx context.Context,
	numSubnets uint32,
) ([]ids.ID, error) {
	fmt.Println()
	ln.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("create subnets")))

	clientURI, err := ln.getClientURI()
	if err != nil {
		return nil, err
	}
	platformCli := platformvm.NewClient(clientURI)

	pTXs := []ids.ID{}
	baseWallet, avaxAssetID, testKeyAddr, err := setupWallet(ctx, clientURI, pTXs, ln.log)
	if err != nil {
		return nil, err
	}

	if err := ln.addPrimaryValidators(ctx, platformCli, baseWallet, testKeyAddr); err != nil {
		return nil, err
	}

	subnetIDs, err := createSubnets(ctx, numSubnets, baseWallet, testKeyAddr, ln.log)
	if err != nil {
		return nil, err
	}

	baseWallet, err = ln.restartNodesAndResetWallet(ctx, subnetIDs, pTXs, clientURI)
	if err != nil {
		return nil, err
	}

	if err = ln.addSubnetValidators(ctx, platformCli, baseWallet, subnetIDs); err != nil {
		return nil, err
	}

	if err = ln.waitSubnetValidators(ctx, platformCli, subnetIDs); err != nil {
		return nil, err
	}

	fmt.Println()
	ln.log.Info(logging.Green.Wrap("checking the remaining balance of the base wallet"))
	balances, err := baseWallet.P().Builder().GetBalance()
	if err != nil {
		return nil, err
	}
	ln.log.Info("base wallet AVAX balance", zap.Uint64("balance", balances[avaxAssetID]), zap.String("address", testKeyAddr.String()))

	return subnetIDs, nil
}

func (ln *localNetwork) restartNodesAndResetWallet(
	ctx context.Context,
	subnetIDs []ids.ID,
	pTXs []ids.ID,
	clientURI string,
) (primary.Wallet, error) {
	fmt.Println()
	ln.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("restarting network")))
	if err := ln.restartNodesWithWhitelistedSubnets(ctx, subnetIDs); err != nil {
		return nil, err
	}
	fmt.Println()
	ln.log.Info(logging.Green.Wrap("reconnecting the wallet client after restart"))
	testKeychain := secp256k1fx.NewKeychain(genesis.EWOQKey)
	allTxs := append(pTXs, subnetIDs...)
	return primary.NewWalletWithTxs(ctx, clientURI, testKeychain, allTxs...)
}

func (ln *localNetwork) waitForCustomChainsReady(
	ctx context.Context,
	chainInfos []blockchainInfo,
) error {
	fmt.Println()
	ln.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("waiting for custom chains to report healthy...")))

	if err := ln.healthy(ctx); err != nil {
		return err
	}

	subnetIDs := []ids.ID{}
	for _, chainInfo := range chainInfos {
		subnetIDs = append(subnetIDs, chainInfo.subnetID)
	}
	clientURI, err := ln.getClientURI()
	if err != nil {
		return err
	}
	platformCli := platformvm.NewClient(clientURI)
	if err := ln.waitSubnetValidators(ctx, platformCli, subnetIDs); err != nil {
		return err
	}

	for nodeName, node := range ln.nodes {
		ln.log.Info("inspecting node log directory for custom chain logs", zap.String("log-dir", node.GetLogsDir()), zap.String("node-name", nodeName))
		for _, chainInfo := range chainInfos {
			p := filepath.Join(node.GetLogsDir(), chainInfo.blockchainID.String()+".log")
			ln.log.Info("checking log",
				zap.String("vm-ID", chainInfo.vmID.String()),
				zap.String("subnet-ID", chainInfo.subnetID.String()),
				zap.String("blockchain-ID", chainInfo.blockchainID.String()),
				zap.String("path", p),
			)
			for {
				_, err := os.Stat(p)
				if err == nil {
					ln.log.Info("found the log", zap.String("path", p))
					break
				}

				ln.log.Info("log not found yet, retrying...",
					zap.String("vm-ID", chainInfo.vmID.String()),
					zap.String("subnet-ID", chainInfo.subnetID.String()),
					zap.String("blockchain-ID", chainInfo.blockchainID.String()),
					zap.Error(err),
				)
				select {
				case <-ln.onStopCh:
					return errAborted
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(blockchainLogPullFrequency):
				}
			}
		}
	}

	fmt.Println()
	ln.log.Info(logging.Green.Wrap("all custom chains are running!!!"))

	fmt.Println()
	ln.log.Info(logging.Green.Wrap(logging.Bold.Wrap("all custom chains are ready on RPC server-side -- network-runner RPC client can poll and query the cluster status")))

	return nil
}

func (ln *localNetwork) getCurrentSubnets(ctx context.Context) ([]ids.ID, error) {
	nonPlatformSubnets := []ids.ID{}
	node := ln.getSomeNode()
	subnets, err := node.GetAPIClient().PChainAPI().GetSubnets(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, subnet := range subnets {
		if subnet.ID != constants.PlatformChainID {
			nonPlatformSubnets = append(nonPlatformSubnets, subnet.ID)
		}
	}
	return nonPlatformSubnets, nil
}

// TODO: make this "restart" pattern more generic, so it can be used for "Restart" RPC
func (ln *localNetwork) restartNodesWithWhitelistedSubnets(
	ctx context.Context,
	subnetIDs []ids.ID,
) (err error) {
	fmt.Println()
	ln.log.Info(logging.Green.Wrap("restarting each node"), zap.String("whitelisted-subnets", config.WhitelistedSubnetsKey))
	whitelistedSubnetIDsMap := map[string]struct{}{}
	currentSubnets, err := ln.getCurrentSubnets(ctx)
	if err != nil {
		return err
	}
	for _, subnet := range currentSubnets {
		whitelistedSubnetIDsMap[subnet.String()] = struct{}{}
	}
	for _, subnetID := range subnetIDs {
		whitelistedSubnetIDsMap[subnetID.String()] = struct{}{}
	}
	whitelistedSubnetIDs := []string{}
	for subnetID := range whitelistedSubnetIDsMap {
		whitelistedSubnetIDs = append(whitelistedSubnetIDs, subnetID)
	}
	sort.Strings(whitelistedSubnetIDs)
	whitelistedSubnets := strings.Join(whitelistedSubnetIDs, ",")

	ln.log.Info("restarting all nodes to whitelist subnets", zap.Strings("whitelisted-subnet-IDs", whitelistedSubnetIDs))

	// change default setting
	ln.flags[config.WhitelistedSubnetsKey] = whitelistedSubnets

	for nodeName, node := range ln.nodes {
		// delete node specific flag so as to use default one
		nodeConfig := node.GetConfig()
		delete(nodeConfig.Flags, config.WhitelistedSubnetsKey)

		ln.log.Info("removing and adding back the node for whitelisted subnets", zap.String("node-name", nodeName))
		if err := ln.restartNode(ctx, nodeName, "", "", nil, nil, nil); err != nil {
			return err
		}

		ln.log.Info("waiting for local cluster readiness after restarting node", zap.String("node-name", nodeName))
		if err := ln.healthy(ctx); err != nil {
			return err
		}
	}
	return nil
}

func setupWallet(
	ctx context.Context,
	clientURI string,
	pTXs []ids.ID,
	log logging.Logger,
) (baseWallet primary.Wallet, avaxAssetID ids.ID, testKeyAddr ids.ShortID, err error) {
	// "local/default/genesis.json" pre-funds "ewoq" key
	testKey := genesis.EWOQKey
	testKeyAddr = testKey.PublicKey().Address()
	testKeychain := secp256k1fx.NewKeychain(genesis.EWOQKey)

	fmt.Println()
	log.Info(logging.Green.Wrap("setting up the base wallet with the seed test key"))

	baseWallet, err = primary.NewWalletWithTxs(ctx, clientURI, testKeychain, pTXs...)
	if err != nil {
		return nil, ids.Empty, ids.ShortEmpty, err
	}
	log.Info("set up base wallet with pre-funded test key address", zap.String("endpoint", clientURI), zap.String("address", testKeyAddr.String()))

	fmt.Println()
	log.Info(logging.Green.Wrap("check if the seed test key has enough balance to create validators and subnets"))
	avaxAssetID = baseWallet.P().AVAXAssetID()
	balances, err := baseWallet.P().Builder().GetBalance()
	if err != nil {
		return nil, ids.Empty, ids.ShortEmpty, err
	}
	bal, ok := balances[avaxAssetID]
	if bal <= 1*units.Avax || !ok {
		return nil, ids.Empty, ids.ShortEmpty, fmt.Errorf("not enough AVAX balance %v in the address %q", bal, testKeyAddr)
	}
	log.Info("fetched base wallet", zap.String("api", clientURI), zap.Uint64("balance", bal), zap.String("address", testKeyAddr.String()))

	return baseWallet, avaxAssetID, testKeyAddr, nil
}

// add the nodes in [nodeInfos] as validators of the primary network, in case they are not
// the validation starts as soon as possible and its duration is as long as possible, that is,
// it is set to max accepted duration by avalanchego
func (ln *localNetwork) addPrimaryValidators(
	ctx context.Context,
	platformCli platformvm.Client,
	baseWallet primary.Wallet,
	testKeyAddr ids.ShortID,
) error {
	ln.log.Info(logging.Green.Wrap("adding the nodes as primary network validators"))
	// ref. https://docs.avax.network/build/avalanchego-apis/p-chain/#platformgetcurrentvalidators
	cctx, cancel := createDefaultCtx(ctx)
	vs, err := platformCli.GetCurrentValidators(cctx, constants.PrimaryNetworkID, nil)
	cancel()
	if err != nil {
		return err
	}
	curValidators := make(map[ids.NodeID]struct{})
	for _, v := range vs {
		curValidators[v.NodeID] = struct{}{}
	}
	for nodeName, node := range ln.nodes {
		nodeID := node.GetNodeID()

		_, isValidator := curValidators[nodeID]
		if isValidator {
			continue
		}

		cctx, cancel = createDefaultCtx(ctx)
		txID, err := baseWallet.P().IssueAddValidatorTx(
			&validator.Validator{
				NodeID: nodeID,
				Start:  uint64(time.Now().Add(validationStartOffset).Unix()),
				End:    uint64(time.Now().Add(validationDuration).Unix()),
				Wght:   genesis.LocalParams.MinValidatorStake,
			},
			&secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{testKeyAddr},
			},
			10*10000, // 10% fee percent, times 10000 to make it as shares
			common.WithContext(cctx),
			defaultPoll,
		)
		cancel()
		if err != nil {
			return err
		}
		ln.log.Info("added node as primary subnet validator", zap.String("node-name", nodeName), zap.String("node-ID", nodeID.String()), zap.String("tx-ID", txID.String()))
	}
	return nil
}

func createSubnets(
	ctx context.Context,
	numSubnets uint32,
	baseWallet primary.Wallet,
	testKeyAddr ids.ShortID,
	log logging.Logger,
) ([]ids.ID, error) {
	fmt.Println()
	log.Info(logging.Green.Wrap("creating subnets VM"), zap.Uint32("num-subnets", numSubnets))
	subnetIDs := make([]ids.ID, numSubnets)
	var i uint32
	for i = 0; i < numSubnets; i++ {
		log.Info("creating subnet tx")
		cctx, cancel := createDefaultCtx(ctx)
		subnetID, err := baseWallet.P().IssueCreateSubnetTx(
			&secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{testKeyAddr},
			},
			common.WithContext(cctx),
			defaultPoll,
		)
		cancel()
		if err != nil {
			return nil, err
		}
		log.Info("created subnet tx", zap.String("subnet-ID", subnetID.String()))
		subnetIDs[i] = subnetID
	}
	return subnetIDs, nil
}

// add the nodes in [nodeInfos] as validators of the given subnets, in case they are not
// the validation starts as soon as possible and its duration is as long as possible, that is,
// it ends at the time the primary network validation ends for the node
func (ln *localNetwork) addSubnetValidators(
	ctx context.Context,
	platformCli platformvm.Client,
	baseWallet primary.Wallet,
	subnetIDs []ids.ID,
) error {
	ln.log.Info(logging.Green.Wrap("adding the nodes as subnet validators"))
	for _, subnetID := range subnetIDs {
		cctx, cancel := createDefaultCtx(ctx)
		vs, err := platformCli.GetCurrentValidators(cctx, constants.PrimaryNetworkID, nil)
		cancel()
		if err != nil {
			return err
		}
		primaryValidatorsEndtime := make(map[ids.NodeID]time.Time)
		for _, v := range vs {
			primaryValidatorsEndtime[v.NodeID] = time.Unix(int64(v.EndTime), 0)
		}
		cctx, cancel = createDefaultCtx(ctx)
		vs, err = platformCli.GetCurrentValidators(cctx, subnetID, nil)
		cancel()
		if err != nil {
			return err
		}
		subnetValidators := set.Set[ids.NodeID]{}
		for _, v := range vs {
			subnetValidators.Add(v.NodeID)
		}
		for nodeName, node := range ln.nodes {
			nodeID := node.GetNodeID()
			isValidator := subnetValidators.Contains(nodeID)
			if isValidator {
				continue
			}
			cctx, cancel := createDefaultCtx(ctx)
			txID, err := baseWallet.P().IssueAddSubnetValidatorTx(
				&validator.SubnetValidator{
					Validator: validator.Validator{
						NodeID: nodeID,
						// reasonable delay in most/slow test environments
						Start: uint64(time.Now().Add(validationStartOffset).Unix()),
						End:   uint64(primaryValidatorsEndtime[nodeID].Unix()),
						Wght:  subnetValidatorsWeight,
					},
					Subnet: subnetID,
				},
				common.WithContext(cctx),
				defaultPoll,
			)
			cancel()
			if err != nil {
				return err
			}
			ln.log.Info("added node as a subnet validator to subnet",
				zap.String("node-name", nodeName),
				zap.String("node-ID", nodeID.String()),
				zap.String("subnet-ID", subnetID.String()),
				zap.String("tx-ID", txID.String()),
			)
		}
	}
	return nil
}

// waits until all nodes in [nodeInfos] start validating the given [subnetIDs]
func (ln *localNetwork) waitSubnetValidators(
	ctx context.Context,
	platformCli platformvm.Client,
	subnetIDs []ids.ID,
) error {
	ln.log.Info(logging.Green.Wrap("waiting for the nodes to become subnet validators"))
	for {
		ready := true
		for _, subnetID := range subnetIDs {
			cctx, cancel := createDefaultCtx(ctx)
			vs, err := platformCli.GetCurrentValidators(cctx, subnetID, nil)
			cancel()
			if err != nil {
				return err
			}
			subnetValidators := set.Set[ids.NodeID]{}
			for _, v := range vs {
				subnetValidators.Add(v.NodeID)
			}
			for _, node := range ln.nodes {
				nodeID := node.GetNodeID()
				if isValidator := subnetValidators.Contains(nodeID); !isValidator {
					ready = false
				}
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ln.onStopCh:
			return errAborted
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitForValidatorsPullFrequency):
		}
	}
}

// reload VM plugins on all nodes
func (ln *localNetwork) reloadVMPlugins(
	ctx context.Context,
) error {
	ln.log.Info(logging.Green.Wrap("reloading plugin binaries"))
	for _, node := range ln.nodes {
		uri := fmt.Sprintf("http://%s:%d", node.GetURL(), node.GetAPIPort())
		adminCli := admin.NewClient(uri)
		cctx, cancel := createDefaultCtx(ctx)
		_, failedVMs, err := adminCli.LoadVMs(cctx)
		cancel()
		if err != nil {
			return err
		}
		if len(failedVMs) > 0 {
			return fmt.Errorf("%d VMs failed to load: %v\n", len(failedVMs), failedVMs)
		}
	}
	return nil
}

func createBlockchainTxs(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
	baseWallet primary.Wallet,
	log logging.Logger,
) ([]*txs.Tx, error) {
	fmt.Println()
	log.Info(logging.Green.Wrap("creating tx for each custom chain"))
	blockchainTxs := make([]*txs.Tx, len(chainSpecs))
	for i, chainSpec := range chainSpecs {
		vmName := chainSpec.VmName
		vmID, err := utils.VMID(vmName)
		if err != nil {
			return nil, err
		}
		genesisBytes := chainSpec.Genesis

		log.Info("creating blockchain tx",
			zap.String("vm-name", vmName),
			zap.String("vm-ID", vmID.String()),
			zap.Int("bytes length of genesis", len(genesisBytes)),
		)
		cctx, cancel := createDefaultCtx(ctx)
		defer cancel()
		subnetID, err := ids.FromString(*chainSpec.SubnetId)
		if err != nil {
			return nil, err
		}
		utx, err := baseWallet.P().Builder().NewCreateChainTx(
			subnetID,
			genesisBytes,
			vmID,
			nil,
			vmName,
		)
		if err != nil {
			return nil, fmt.Errorf("failure generating create blockchain tx: %w", err)
		}
		tx, err := baseWallet.P().Signer().SignUnsigned(cctx, utx)
		if err != nil {
			return nil, fmt.Errorf("failure signing create blockchain tx: %w", err)
		}

		blockchainTxs[i] = tx
	}

	return blockchainTxs, nil
}

func (ln *localNetwork) createBlockchainConfigFiles(
	chainSpecs []network.BlockchainSpec,
	blockchainTxs []*txs.Tx,
	log logging.Logger,
) (bool, error) {
	fmt.Println()
	created := false
	log.Info(logging.Green.Wrap("creating config files for each custom chain"))
	for i, chainSpec := range chainSpecs {
		chainAlias := blockchainTxs[i].ID().String()

		// create config, network upgrade and subnet config files
		if chainSpec.ChainConfig != nil {
			created = true
			for nodeName, node := range ln.nodes {
				nodeChainConfig := chainSpec.ChainConfig
				if conf, ok := chainSpec.PerNodeChainConfig[nodeName]; ok {
					// keep contents to write file
					nodeChainConfig = conf
					// update node config for state preservation
					if node.config.ChainConfigFiles == nil {
						node.config.ChainConfigFiles = map[string]string{}
					}
					node.config.ChainConfigFiles[chainAlias] = string(conf)
				}
				nodeRootDir := getNodeDir(ln.rootDir, nodeName)
				chainConfigDir := filepath.Join(nodeRootDir, chainConfigSubDir)
				chainConfigPath := filepath.Join(chainConfigDir, chainAlias, configFileName)
				if err := createFileAndWrite(chainConfigPath, nodeChainConfig); err != nil {
					return false, fmt.Errorf("couldn't write chain config file at %q: %w", chainConfigPath, err)
				}
			}
		}
		if chainSpec.NetworkUpgrade != nil {
			created = true
			for nodeName := range ln.nodes {
				nodeRootDir := getNodeDir(ln.rootDir, nodeName)
				chainConfigDir := filepath.Join(nodeRootDir, chainConfigSubDir)
				chainUpgradePath := filepath.Join(chainConfigDir, chainAlias, upgradeConfigFileName)
				if err := createFileAndWrite(chainUpgradePath, chainSpec.NetworkUpgrade); err != nil {
					return false, fmt.Errorf("couldn't write network upgrade file at %q: %w", chainUpgradePath, err)
				}
			}
		}
		if chainSpec.SubnetConfig != nil {
			created = true
			for nodeName := range ln.nodes {
				nodeRootDir := getNodeDir(ln.rootDir, nodeName)
				subnetConfigDir := filepath.Join(nodeRootDir, subnetConfigSubDir)
				subnetConfigPath := filepath.Join(subnetConfigDir, *chainSpec.SubnetId+".json")
				if err := createFileAndWrite(subnetConfigPath, chainSpec.SubnetConfig); err != nil {
					return false, fmt.Errorf("couldn't write chain config file at %q: %w", subnetConfigPath, err)
				}
			}
		}
		// update config info for snapshopt/restart purposes
		// put into defaults and reset node specifics
		if chainSpec.ChainConfig != nil {
			ln.chainConfigFiles[chainAlias] = string(chainSpec.ChainConfig)
			for nodeName := range ln.nodes {
				delete(ln.nodes[nodeName].config.ChainConfigFiles, chainAlias)
			}
		}
		if chainSpec.NetworkUpgrade != nil {
			ln.upgradeConfigFiles[chainAlias] = string(chainSpec.NetworkUpgrade)
			for nodeName := range ln.nodes {
				delete(ln.nodes[nodeName].config.UpgradeConfigFiles, chainAlias)
			}
		}
		if chainSpec.SubnetConfig != nil {
			ln.subnetConfigFiles[*chainSpec.SubnetId] = string(chainSpec.SubnetConfig)
			for nodeName := range ln.nodes {
				delete(ln.nodes[nodeName].config.SubnetConfigFiles, *chainSpec.SubnetId)
			}
		}
	}
	return created, nil
}

func (ln *localNetwork) createBlockchains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
	blockchainTxs []*txs.Tx,
	baseWallet primary.Wallet,
	log logging.Logger,
) error {
	fmt.Println()
	log.Info(logging.Green.Wrap("creating each custom chain"))
	for i, chainSpec := range chainSpecs {
		vmName := chainSpec.VmName
		vmID, err := utils.VMID(vmName)
		if err != nil {
			return err
		}
		log.Info("creating blockchain",
			zap.String("vm-name", vmName),
			zap.String("vm-ID", vmID.String()),
		)

		cctx, cancel := createDefaultCtx(ctx)
		defer cancel()

		blockchainID, err := baseWallet.P().IssueTx(
			blockchainTxs[i],
			common.WithContext(cctx),
			defaultPoll,
		)
		if err != nil {
			return fmt.Errorf("failure issuing create blockchain: %w", err)
		}
		if blockchainID != blockchainTxs[i].ID() {
			return fmt.Errorf("failure issuing create blockchain: txID differs from blockchaindID")
		}

		log.Info("created a new blockchain",
			zap.String("vm-name", vmName),
			zap.String("vm-ID", vmID.String()),
			zap.String("blockchain-ID", blockchainID.String()),
		)
	}

	return nil
}

func createDefaultCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, defaultTimeout)
}
