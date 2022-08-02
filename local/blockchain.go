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
	"github.com/ava-labs/avalanche-network-runner/pkg/color"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/api/admin"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/platformvm"
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
	// check period for blockchain logs while waiting for custom VMs to be ready
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
	vmName       string
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
	chainInfos, err := ln.installCustomVMs(ctx, chainSpecs)
	if err != nil {
		return err
	}

	if err := ln.waitForCustomVMsReady(ctx, chainInfos); err != nil {
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

// provisions local cluster and install custom VMs if applicable
// assumes the local cluster is already set up and healthy
func (ln *localNetwork) installCustomVMs(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
) ([]blockchainInfo, error) {
	println()
	color.Outf("{{blue}}{{bold}}create and install custom VMs{{/}}\n")

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

	baseWallet, avaxAssetID, testKeyAddr, err := setupWallet(ctx, clientURI, pTXs)
	if err != nil {
		return nil, err
	}

	// get number of subnets to create
	// for the list of requested blockchains, we count those that have undefined subnet id
	// that number of subnets will be created and later assigned to those blockchain requests
	var numSubnets uint32
	for _, chainSpec := range chainSpecs {
		if chainSpec.SubnetId == nil {
			numSubnets++
		}
	}

	if err := ln.addPrimaryValidators(ctx, platformCli, baseWallet, testKeyAddr); err != nil {
		return nil, err
	}

	if numSubnets > 0 {
		var addedSubnetIDs []ids.ID
		// add missing subnets, restarting network and waiting for subnet validation to start
		baseWallet, addedSubnetIDs, err = ln.installSubnets(ctx, numSubnets, baseWallet, testKeyAddr, pTXs)
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
	}

	subnetIDs := []ids.ID{}
	for _, chainSpec := range chainSpecs {
		subnetID, err := ids.FromString(*chainSpec.SubnetId)
		if err != nil {
			return nil, err
		}
		subnetIDs = append(subnetIDs, subnetID)
	}
	clientURI, err = ln.getClientURI()
	if err != nil {
		return nil, err
	}
	platformCli = platformvm.NewClient(clientURI)
	if err = ln.addSubnetValidators(ctx, platformCli, baseWallet, subnetIDs); err != nil {
		return nil, err
	}

	if err := ln.reloadVMPlugins(ctx); err != nil {
		return nil, err
	}

	blockchainIDs, err := createBlockchains(ctx, chainSpecs, baseWallet, testKeyAddr)
	if err != nil {
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
			vmName:       chainSpec.VmName,
			vmID:         vmID,
			subnetID:     subnetID,
			blockchainID: blockchainIDs[i],
		}
	}

	println()
	color.Outf("{{green}}checking the remaining balance of the base wallet{{/}}\n")
	balances, err := baseWallet.P().Builder().GetBalance()
	if err != nil {
		return nil, err
	}
	zap.L().Info("base wallet AVAX balance",
		zap.String("address", testKeyAddr.String()),
		zap.Uint64("balance", balances[avaxAssetID]),
	)

	return chainInfos, nil
}

func (ln *localNetwork) setupWalletAndInstallSubnets(
	ctx context.Context,
	numSubnets uint32,
) ([]ids.ID, error) {
	println()
	color.Outf("{{blue}}{{bold}}create subnets{{/}}\n")

	clientURI, err := ln.getClientURI()
	if err != nil {
		return nil, err
	}
	platformCli := platformvm.NewClient(clientURI)

	pTXs := []ids.ID{}
	baseWallet, avaxAssetID, testKeyAddr, err := setupWallet(ctx, clientURI, pTXs)
	if err != nil {
		return nil, err
	}

	if err := ln.addPrimaryValidators(ctx, platformCli, baseWallet, testKeyAddr); err != nil {
		return nil, err
	}

	// add subnets restarting network if necessary
	baseWallet, subnetIDs, err := ln.installSubnets(ctx, numSubnets, baseWallet, testKeyAddr, pTXs)
	if err != nil {
		return nil, err
	}

	clientURI, err = ln.getClientURI()
	if err != nil {
		return nil, err
	}
	platformCli = platformvm.NewClient(clientURI)
	if err = ln.addSubnetValidators(ctx, platformCli, baseWallet, subnetIDs); err != nil {
		return nil, err
	}

	if err = ln.waitSubnetValidators(ctx, platformCli, subnetIDs); err != nil {
		return nil, err
	}

	println()
	color.Outf("{{green}}checking the remaining balance of the base wallet{{/}}\n")
	balances, err := baseWallet.P().Builder().GetBalance()
	if err != nil {
		return nil, err
	}
	zap.L().Info("base wallet AVAX balance",
		zap.String("address", testKeyAddr.String()),
		zap.Uint64("balance", balances[avaxAssetID]),
	)

	return subnetIDs, nil
}

func (ln *localNetwork) installSubnets(
	ctx context.Context,
	numSubnets uint32,
	baseWallet primary.Wallet,
	testKeyAddr ids.ShortID,
	pTXs []ids.ID,
) (primary.Wallet, []ids.ID, error) {
	println()
	color.Outf("{{blue}}{{bold}}add subnets{{/}}\n")

	subnetIDs, err := createSubnets(ctx, numSubnets, baseWallet, testKeyAddr)
	if err != nil {
		return nil, nil, err
	}
	if numSubnets > 0 {
		if err = ln.restartNodesWithWhitelistedSubnets(ctx, subnetIDs); err != nil {
			return nil, nil, err
		}
		println()
		color.Outf("{{green}}reconnecting the wallet client after restart{{/}}\n")
		clientURI, err := ln.getClientURI()
		if err != nil {
			return nil, nil, err
		}
		testKeychain := secp256k1fx.NewKeychain(genesis.EWOQKey)
		allTxs := append(pTXs, subnetIDs...)
		baseWallet, err = primary.NewWalletWithTxs(ctx, clientURI, testKeychain, allTxs...)
		if err != nil {
			return nil, nil, err
		}
		zap.L().Info("set up base wallet with pre-funded test key",
			zap.String("http-rpc-endpoint", clientURI),
			zap.String("address", testKeyAddr.String()),
		)
	}
	return baseWallet, subnetIDs, nil
}

func (ln *localNetwork) waitForCustomVMsReady(
	ctx context.Context,
	chainInfos []blockchainInfo,
) error {
	println()
	color.Outf("{{blue}}{{bold}}waiting for custom VMs to report healthy...{{/}}\n")

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
		zap.L().Info("inspecting node log directory for custom VM logs",
			zap.String("node-name", nodeName),
			zap.String("log-dir", node.GetLogsDir()),
		)
		for _, chainInfo := range chainInfos {
			p := filepath.Join(node.GetLogsDir(), chainInfo.blockchainID.String()+".log")
			zap.L().Info("checking log",
				zap.String("vm-id", chainInfo.vmID.String()),
				zap.String("subnet-id", chainInfo.subnetID.String()),
				zap.String("blockchain-id", chainInfo.blockchainID.String()),
				zap.String("log-path", p),
			)
			for {
				_, err := os.Stat(p)
				if err == nil {
					zap.L().Info("found the log", zap.String("log-path", p))
					break
				}

				zap.L().Info("log not found yet, retrying...",
					zap.String("vm-id", chainInfo.vmID.String()),
					zap.String("subnet-id", chainInfo.subnetID.String()),
					zap.String("blockchain-id", chainInfo.blockchainID.String()),
					zap.String("log-path", p),
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

	println()
	color.Outf("{{green}}{{bold}}all custom VMs are running!!!{{/}}\n")

	println()
	color.Outf("{{green}}{{bold}}all custom VMs are ready on RPC server-side -- network-runner RPC client can poll and query the cluster status{{/}}\n")

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
	println()
	color.Outf("{{green}}restarting each node with %s{{/}}\n", config.WhitelistedSubnetsKey)
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

	zap.L().Info("restarting all nodes to whitelist subnet",
		zap.Strings("whitelisted-subnets", whitelistedSubnetIDs),
	)
	for nodeName, node := range ln.nodes {
		// replace WhitelistedSubnetsKey flag
		nodeConfig := node.GetConfig()
		nodeConfig.ConfigFile, err = utils.SetJSONKey(nodeConfig.ConfigFile, config.WhitelistedSubnetsKey, whitelistedSubnets)
		if err != nil {
			return err
		}

		zap.L().Info("removing and adding back the node for whitelisted subnets", zap.String("node-name", nodeName))
		if err := ln.removeNode(ctx, nodeName); err != nil {
			return err
		}

		if _, err := ln.addNode(nodeConfig); err != nil {
			return err
		}

		zap.L().Info("waiting for local cluster readiness after restart", zap.String("node-name", nodeName))
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
) (baseWallet primary.Wallet, avaxAssetID ids.ID, testKeyAddr ids.ShortID, err error) {
	// "local/default/genesis.json" pre-funds "ewoq" key
	testKey := genesis.EWOQKey
	testKeyAddr = testKey.PublicKey().Address()
	testKeychain := secp256k1fx.NewKeychain(genesis.EWOQKey)

	println()
	color.Outf("{{green}}setting up the base wallet with the seed test key{{/}}\n")

	baseWallet, err = primary.NewWalletWithTxs(ctx, clientURI, testKeychain, pTXs...)
	if err != nil {
		return nil, ids.Empty, ids.ShortEmpty, err
	}
	zap.L().Info("set up base wallet with pre-funded test key",
		zap.String("http-rpc-endpoint", clientURI),
		zap.String("address", testKeyAddr.String()),
	)

	println()
	color.Outf("{{green}}check if the seed test key has enough balance to create validators and subnets{{/}}\n")
	avaxAssetID = baseWallet.P().AVAXAssetID()
	balances, err := baseWallet.P().Builder().GetBalance()
	if err != nil {
		return nil, ids.Empty, ids.ShortEmpty, err
	}
	bal, ok := balances[avaxAssetID]
	if bal <= 1*units.Avax || !ok {
		return nil, ids.Empty, ids.ShortEmpty, fmt.Errorf("not enough AVAX balance %v in the address %q", bal, testKeyAddr)
	}
	zap.L().Info("fetched base wallet AVAX balance",
		zap.String("http-rpc-endpoint", clientURI),
		zap.String("address", testKeyAddr.String()),
		zap.Uint64("balance", bal),
	)

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
	color.Outf("{{green}}adding the nodes as primary network validators{{/}}\n")
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
				Wght:   1 * units.Avax,
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
		zap.L().Info("added the node as primary subnet validator",
			zap.String("node-name", nodeName),
			zap.String("node-id", nodeID.String()),
			zap.String("tx-id", txID.String()),
		)
	}
	return nil
}

func createSubnets(
	ctx context.Context,
	numSubnets uint32,
	baseWallet primary.Wallet,
	testKeyAddr ids.ShortID,
) ([]ids.ID, error) {
	println()
	color.Outf("{{green}}creating %d subnets VM{{/}}\n", numSubnets)
	subnetIDs := make([]ids.ID, numSubnets)
	var i uint32
	for i = 0; i < numSubnets; i++ {
		zap.L().Info("creating subnet tx")
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
		zap.L().Info("created subnet tx", zap.String("subnet-id", subnetID.String()))
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
	color.Outf("{{green}}adding the nodes as subnet validators{{/}}\n")
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
		subnetValidators := ids.NodeIDSet{}
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
			zap.L().Info("added the node as a subnet validator",
				zap.String("subnet-id", subnetID.String()),
				zap.String("node-name", nodeName),
				zap.String("node-id", nodeID.String()),
				zap.String("tx-id", txID.String()),
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
	color.Outf("{{green}}waiting for the nodes to become subnet validators{{/}}\n")
	for {
		ready := true
		for _, subnetID := range subnetIDs {
			cctx, cancel := createDefaultCtx(ctx)
			vs, err := platformCli.GetCurrentValidators(cctx, subnetID, nil)
			cancel()
			if err != nil {
				return err
			}
			subnetValidators := ids.NodeIDSet{}
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
	color.Outf("{{green}}reloading plugin binaries{{/}}\n")
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

func createBlockchains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
	baseWallet primary.Wallet,
	testKeyAddr ids.ShortID,
) ([]ids.ID, error) {
	println()
	color.Outf("{{green}}creating blockchain for each custom VM{{/}}\n")
	blockchainIDs := make([]ids.ID, len(chainSpecs))
	for i, chainSpec := range chainSpecs {
		vmName := chainSpec.VmName
		vmID, err := utils.VMID(vmName)
		if err != nil {
			return nil, err
		}
		vmGenesisBytes := chainSpec.Genesis

		zap.L().Info("creating blockchain tx",
			zap.String("vm-name", vmName),
			zap.String("vm-id", vmID.String()),
			zap.Int("genesis-bytes", len(vmGenesisBytes)),
		)
		cctx, cancel := createDefaultCtx(ctx)
		subnetID, err := ids.FromString(*chainSpec.SubnetId)
		if err != nil {
			return nil, err
		}
		blockchainID, err := baseWallet.P().IssueCreateChainTx(
			subnetID,
			vmGenesisBytes,
			vmID,
			nil,
			vmName,
			common.WithContext(cctx),
			defaultPoll,
		)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("failure creating blockchain: %w", err)
		}

		blockchainIDs[i] = blockchainID

		zap.L().Info("created a new blockchain",
			zap.String("vm-name", vmName),
			zap.String("vm-id", vmID.String()),
			zap.String("blockchain-id", blockchainID.String()),
		)
	}

	return blockchainIDs, nil
}

func createDefaultCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, defaultTimeout)
}
