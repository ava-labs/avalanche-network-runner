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
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/chain/p"
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
		if n.paused {
			continue
		}
		node = n
		break
	}
	return node
}

// get node client URI for an arbitrary node in the network
func (ln *localNetwork) getClientURI() (string, error) { //nolint
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

	if err := ln.RegisterBlockchainAliases(ctx, chainInfos, chainSpecs); err != nil {
		return err
	}

	return nil
}

// if alias is defined in blockchain-specs, registers an alias for the previously created blockchain
func (ln *localNetwork) RegisterBlockchainAliases(
	ctx context.Context,
	chainInfos []blockchainInfo,
	chainSpecs []network.BlockchainSpec,
) error {
	fmt.Println()
	ln.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("registering blockchain aliases")))
	for i, chainSpec := range chainSpecs {
		if chainSpec.BlockchainAlias == "" {
			continue
		}
		blockchainAlias := chainSpec.BlockchainAlias
		chainID := chainInfos[i].blockchainID.String()
		ln.log.Info("registering blockchain alias",
			zap.String("alias", blockchainAlias),
			zap.String("chain-id", chainID))
		for nodeName, node := range ln.nodes {
			if node.paused {
				continue
			}
			if err := node.client.AdminAPI().AliasChain(ctx, chainID, blockchainAlias); err != nil {
				return fmt.Errorf("failure to register blockchain alias %v on node %v: %w", blockchainAlias, nodeName, err)
			}
		}
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
	var preloadTXs []ids.ID
	for _, chainSpec := range chainSpecs {
		// if subnet id for the blockchain is specified, we need to add the subnet id
		// tx info to the wallet so blockchain creation does not fail
		// if subnet id is not specified, a new subnet will later be created by using the wallet,
		// and the wallet will obtain the tx info at that moment
		if chainSpec.SubnetID != nil {
			subnetID, err := ids.FromString(*chainSpec.SubnetID)
			if err != nil {
				return nil, err
			}
			preloadTXs = append(preloadTXs, subnetID)
		}
	}

	baseWallet, avaxAssetID, testKeyAddr, err := setupWallet(ctx, clientURI, preloadTXs, ln.log)
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
		if chainSpec.SubnetID == nil {
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
		if chainSpecs[i].SubnetID == nil {
			subnetIDStr := addedSubnetIDs[j].String()
			chainSpecs[i].SubnetID = &subnetIDStr
			j++
		}
	}

	simPWallet, simPBackend, err := setupSimulatedPWallet(ctx, clientURI, append(preloadTXs, addedSubnetIDs...))
	if err != nil {
		return nil, err
	}

	blockchainTxs, err := createBlockchainTxs(ctx, chainSpecs, simPWallet, simPBackend, ln.log)
	if err != nil {
		return nil, err
	}

	blockchainFilesCreated := ln.setBlockchainConfigFiles(chainSpecs, blockchainTxs, ln.log)

	if numSubnetsToCreate > 0 || blockchainFilesCreated {
		// we need to restart if there are new subnets or if there are new network config files
		// add missing subnets, restarting network and waiting for subnet validation to start
		baseWallet, err = ln.restartNodesAndResetWallet(ctx, addedSubnetIDs, preloadTXs, clientURI)
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
		subnetID, err := ids.FromString(*chainSpec.SubnetID)
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
		vmID, err := utils.VMID(chainSpec.VMName)
		if err != nil {
			return nil, err
		}
		subnetID, err := ids.FromString(*chainSpec.SubnetID)
		if err != nil {
			return nil, err
		}
		chainInfos[i] = blockchainInfo{
			// we keep a record of VM name in blockchain name field,
			// as there is no way to recover VM name from VM ID
			chainName:    chainSpec.VMName,
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

	preloadTXs := []ids.ID{}
	baseWallet, avaxAssetID, testKeyAddr, err := setupWallet(ctx, clientURI, preloadTXs, ln.log)
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

	baseWallet, err = ln.restartNodesAndResetWallet(ctx, subnetIDs, preloadTXs, clientURI)
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
	preloadTXs []ids.ID,
	clientURI string,
) (primary.Wallet, error) {
	fmt.Println()
	ln.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("restarting network")))
	if err := ln.restartNodesWithTrackSubnets(ctx, subnetIDs); err != nil {
		return nil, err
	}
	fmt.Println()
	ln.log.Info(logging.Green.Wrap("reconnecting the wallet client after restart"))
	testKeychain := secp256k1fx.NewKeychain(genesis.EWOQKey)
	preloadTXs = append(preloadTXs, subnetIDs...)
	return primary.NewWalletWithTxs(ctx, clientURI, testKeychain, preloadTXs...)
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
		if node.paused {
			continue
		}
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
				if _, err := os.Stat(p); err == nil {
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
	node := ln.getSomeNode()
	subnets, err := node.GetAPIClient().PChainAPI().GetSubnets(ctx, nil)
	if err != nil {
		return nil, err
	}
	nonPlatformSubnets := []ids.ID{}
	for _, subnet := range subnets {
		if subnet.ID != constants.PlatformChainID {
			nonPlatformSubnets = append(nonPlatformSubnets, subnet.ID)
		}
	}
	return nonPlatformSubnets, nil
}

// TODO: make this "restart" pattern more generic, so it can be used for "Restart" RPC
func (ln *localNetwork) restartNodesWithTrackSubnets(
	ctx context.Context,
	subnetIDs []ids.ID,
) (err error) {
	fmt.Println()

	currentSubnets, err := ln.getCurrentSubnets(ctx)
	if err != nil {
		return err
	}

	trackSubnetIDsSet := set.Set[string]{}
	for _, subnetID := range append(currentSubnets, subnetIDs...) {
		trackSubnetIDsSet.Add(subnetID.String())
	}

	trackSubnetIDs := trackSubnetIDsSet.List()
	sort.Strings(trackSubnetIDs)
	trackSubnets := strings.Join(trackSubnetIDs, ",")

	ln.log.Info("restarting all nodes to track subnets", zap.Strings("track-subnet-IDs", trackSubnetIDs))

	// change default setting
	ln.flags[config.TrackSubnetsKey] = trackSubnets

	for nodeName, node := range ln.nodes {
		// delete node specific flag so as to use default one
		nodeConfig := node.GetConfig()
		delete(nodeConfig.Flags, config.TrackSubnetsKey)

		if node.paused {
			continue
		}

		ln.log.Debug("removing and adding back the node for track subnets", zap.String("node-name", nodeName))
		if err := ln.restartNode(ctx, nodeName, "", "", "", nil, nil, nil); err != nil {
			return err
		}

		ln.log.Info("waiting for local cluster readiness after restarting node", zap.String("node-name", nodeName))
		if err := ln.healthy(ctx); err != nil {
			return err
		}
	}
	return nil
}

func setupSimulatedPWallet(
	ctx context.Context,
	uri string,
	preloadTXs []ids.ID,
) (p.Wallet, p.Backend, error) {
	kc := secp256k1fx.NewKeychain(genesis.EWOQKey)
	pCTX, _, utxos, err := primary.FetchState(ctx, uri, kc.Addresses())
	if err != nil {
		return nil, nil, err
	}
	pClient := platformvm.NewClient(uri)
	pTXs := make(map[ids.ID]*txs.Tx)
	for _, id := range preloadTXs {
		txBytes, err := pClient.GetTx(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		tx, err := txs.Parse(txs.Codec, txBytes)
		if err != nil {
			return nil, nil, err
		}
		pTXs[id] = tx
	}
	addrs := kc.Addresses()
	pUTXOs := primary.NewChainUTXOs(constants.PlatformChainID, utxos)
	pBackend := p.NewBackend(pCTX, pUTXOs, pTXs)
	pBuilder := p.NewBuilder(addrs, pBackend)
	pSigner := p.NewSigner(kc, pBackend)
	pWallet := p.NewWallet(pBuilder, pSigner, pClient, pBackend)
	return pWallet, pBackend, nil
}

func setupWallet(
	ctx context.Context,
	clientURI string,
	preloadTXs []ids.ID,
	log logging.Logger,
) (baseWallet primary.Wallet, avaxAssetID ids.ID, testKeyAddr ids.ShortID, err error) {
	// "local/default/genesis.json" pre-funds "ewoq" key
	testKey := genesis.EWOQKey
	testKeyAddr = testKey.PublicKey().Address()
	testKeychain := secp256k1fx.NewKeychain(genesis.EWOQKey)

	fmt.Println()
	log.Info(logging.Green.Wrap("setting up the base wallet with the seed test key"))

	baseWallet, err = primary.NewWalletWithTxs(ctx, clientURI, testKeychain, preloadTXs...)
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
	vdrs, err := platformCli.GetCurrentValidators(cctx, constants.PrimaryNetworkID, nil)
	cancel()
	if err != nil {
		return err
	}
	curValidators := set.Set[ids.NodeID]{}
	for _, v := range vdrs {
		curValidators.Add(v.NodeID)
	}
	for nodeName, node := range ln.nodes {
		nodeID := node.GetNodeID()

		if curValidators.Contains(nodeID) {
			continue
		}

		cctx, cancel = createDefaultCtx(ctx)
		txID, err := baseWallet.P().IssueAddValidatorTx(
			&txs.Validator{
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
	for i := uint32(0); i < numSubnets; i++ {
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
			if isValidator := subnetValidators.Contains(nodeID); isValidator {
				continue
			}
			cctx, cancel := createDefaultCtx(ctx)
			txID, err := baseWallet.P().IssueAddSubnetValidatorTx(
				&txs.SubnetValidator{
					Validator: txs.Validator{
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
			if !ready {
				break
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
func (ln *localNetwork) reloadVMPlugins(ctx context.Context) error {
	ln.log.Info(logging.Green.Wrap("reloading plugin binaries"))
	for _, node := range ln.nodes {
		if node.paused {
			continue
		}
		uri := fmt.Sprintf("http://%s:%d", node.GetURL(), node.GetAPIPort())
		adminCli := admin.NewClient(uri)
		cctx, cancel := createDefaultCtx(ctx)
		_, failedVMs, err := adminCli.LoadVMs(cctx)
		cancel()
		if err != nil {
			return err
		}
		if len(failedVMs) > 0 {
			return fmt.Errorf("%d VMs failed to load: %v", len(failedVMs), failedVMs)
		}
	}
	return nil
}

func createBlockchainTxs(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
	pWallet p.Wallet,
	pBackend p.Backend,
	log logging.Logger,
) ([]*txs.Tx, error) {
	fmt.Println()
	log.Info(logging.Green.Wrap("creating tx for each custom chain"))
	blockchainTxs := make([]*txs.Tx, len(chainSpecs))
	for i, chainSpec := range chainSpecs {
		vmName := chainSpec.VMName
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
		subnetID, err := ids.FromString(*chainSpec.SubnetID)
		if err != nil {
			return nil, err
		}
		utx, err := pWallet.Builder().NewCreateChainTx(
			subnetID,
			genesisBytes,
			vmID,
			nil,
			vmName,
		)
		if err != nil {
			return nil, fmt.Errorf("failure generating create blockchain tx: %w", err)
		}
		tx, err := pWallet.Signer().SignUnsigned(cctx, utx)
		if err != nil {
			return nil, fmt.Errorf("failure signing create blockchain tx: %w", err)
		}
		err = pBackend.AcceptTx(cctx, tx)
		if err != nil {
			return nil, fmt.Errorf("failure accepting create blockchain tx UTXOs: %w", err)
		}

		blockchainTxs[i] = tx
	}

	return blockchainTxs, nil
}

func (ln *localNetwork) setBlockchainConfigFiles(
	chainSpecs []network.BlockchainSpec,
	blockchainTxs []*txs.Tx,
	log logging.Logger,
) bool {
	fmt.Println()
	created := false
	log.Info(logging.Green.Wrap("creating config files for each custom chain"))
	for i, chainSpec := range chainSpecs {
		chainAlias := blockchainTxs[i].ID().String()
		// update config info. set defaults and node specifics
		if chainSpec.ChainConfig != nil || len(chainSpec.PerNodeChainConfig) != 0 {
			created = true
			ln.chainConfigFiles[chainAlias] = string(chainSpec.ChainConfig)
			for nodeName := range ln.nodes {
				if cfg, ok := chainSpec.PerNodeChainConfig[nodeName]; ok {
					ln.nodes[nodeName].config.ChainConfigFiles[chainAlias] = string(cfg)
				} else {
					delete(ln.nodes[nodeName].config.ChainConfigFiles, chainAlias)
				}
			}
		}
		if chainSpec.NetworkUpgrade != nil {
			created = true
			ln.upgradeConfigFiles[chainAlias] = string(chainSpec.NetworkUpgrade)
			for nodeName := range ln.nodes {
				delete(ln.nodes[nodeName].config.UpgradeConfigFiles, chainAlias)
			}
		}
		if chainSpec.SubnetConfig != nil {
			created = true
			ln.subnetConfigFiles[*chainSpec.SubnetID] = string(chainSpec.SubnetConfig)
			for nodeName := range ln.nodes {
				delete(ln.nodes[nodeName].config.SubnetConfigFiles, *chainSpec.SubnetID)
			}
		}
	}
	return created
}

func (*localNetwork) createBlockchains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
	blockchainTxs []*txs.Tx,
	baseWallet primary.Wallet,
	log logging.Logger,
) error {
	fmt.Println()
	log.Info(logging.Green.Wrap("creating each custom chain"))
	for i, chainSpec := range chainSpecs {
		vmName := chainSpec.VMName
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
			return fmt.Errorf("failure issuing create blockchain: txID differs from blockchainID")
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
