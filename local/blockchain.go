// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package local

import (
	"context"
	"encoding/base64"
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
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/chain/p"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary/common"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"
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
	subnetSpecs []network.SubnetSpec,
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()

	if _, err := ln.installSubnets(ctx, subnetSpecs); err != nil {
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

	w, err := newWallet(ctx, clientURI, preloadTXs)
	if err != nil {
		return nil, err
	}

	// get subnet specs for all new subnets to create
	// for the list of requested blockchains, we take those that have undefined subnet id
	// and use the provided subnet spec. if not given, use an empty default subnet spec
	// that subnets will be created and later on assigned to the blockchain requests
	subnetSpecs := []network.SubnetSpec{}
	for _, chainSpec := range chainSpecs {
		if chainSpec.SubnetID == nil {
			if chainSpec.SubnetSpec == nil {
				subnetSpecs = append(subnetSpecs, network.SubnetSpec{})
			} else {
				subnetSpecs = append(subnetSpecs, *chainSpec.SubnetSpec)
			}
		}
	}

	// if no participants are given for a new subnet, assume all nodes should be participants
	allNodeNames := maps.Keys(ln.nodes)
	sort.Strings(allNodeNames)
	for i := range subnetSpecs {
		if len(subnetSpecs[i].Participants) == 0 {
			subnetSpecs[i].Participants = allNodeNames
		}
	}

	// create new nodes
	for _, subnetSpec := range subnetSpecs {
		for _, nodeName := range subnetSpec.Participants {
			_, ok := ln.nodes[nodeName]
			if !ok {
				ln.log.Info(logging.Green.Wrap(fmt.Sprintf("adding new participant %s", nodeName)))
				if _, err := ln.addNode(node.Config{Name: nodeName}); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := ln.healthy(ctx); err != nil {
		return nil, err
	}

	// just ensure all nodes are primary validators (so can be subnet validators)
	if err := ln.addPrimaryValidators(ctx, platformCli, w); err != nil {
		return nil, err
	}

	// create missing subnets
	subnetIDs, err := createSubnets(ctx, uint32(len(subnetSpecs)), w, ln.log)
	if err != nil {
		return nil, err
	}

	if err := ln.setSubnetConfigFiles(subnetIDs, subnetSpecs); err != nil {
		return nil, err
	}

	// assign created subnets to blockchain requests with undefined subnet id
	j := 0
	for i := range chainSpecs {
		if chainSpecs[i].SubnetID == nil {
			subnetIDStr := subnetIDs[j].String()
			chainSpecs[i].SubnetID = &subnetIDStr
			j++
		}
	}

	blockchainTxs, err := createBlockchainTxs(ctx, chainSpecs, w, ln.log)
	if err != nil {
		return nil, err
	}

	blockchainFilesCreated := ln.setBlockchainConfigFiles(chainSpecs, blockchainTxs, ln.log)

	if len(subnetSpecs) > 0 || blockchainFilesCreated {
		// we need to restart if there are new subnets or if there are new network config files
		// add missing subnets, restarting network and waiting for subnet validation to start
		if err := ln.restartNodes(ctx, subnetIDs, subnetSpecs); err != nil {
			return nil, err
		}
	}

	// refresh vm list
	if err := ln.reloadVMPlugins(ctx); err != nil {
		return nil, err
	}

	// create blockchain from txs before spending more utxos
	if err := ln.createBlockchains(ctx, chainSpecs, blockchainTxs, w, ln.log); err != nil {
		return nil, err
	}

	// wait for nodes to be primary validators before trying to add them as subnet ones
	if err = ln.waitPrimaryValidators(ctx, platformCli); err != nil {
		return nil, err
	}

	if err = ln.addSubnetValidators(ctx, platformCli, w, subnetIDs, subnetSpecs); err != nil {
		return nil, err
	}

	if err = ln.waitSubnetValidators(ctx, platformCli, subnetIDs, subnetSpecs); err != nil {
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

	return chainInfos, nil
}

func (ln *localNetwork) installSubnets(
	ctx context.Context,
	subnetSpecs []network.SubnetSpec,
) ([]ids.ID, error) {
	fmt.Println()
	ln.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("create subnets")))

	clientURI, err := ln.getClientURI()
	if err != nil {
		return nil, err
	}
	platformCli := platformvm.NewClient(clientURI)

	w, err := newWallet(ctx, clientURI, []ids.ID{})
	if err != nil {
		return nil, err
	}

	// if no participants are given, assume all nodes should be participants
	allNodeNames := maps.Keys(ln.nodes)
	sort.Strings(allNodeNames)
	for i := range subnetSpecs {
		if len(subnetSpecs[i].Participants) == 0 {
			subnetSpecs[i].Participants = allNodeNames
		}
	}

	// create new nodes
	for _, subnetSpec := range subnetSpecs {
		for _, nodeName := range subnetSpec.Participants {
			_, ok := ln.nodes[nodeName]
			if !ok {
				ln.log.Info(logging.Green.Wrap(fmt.Sprintf("adding new participant %s", nodeName)))
				if _, err := ln.addNode(node.Config{Name: nodeName}); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := ln.healthy(ctx); err != nil {
		return nil, err
	}

	// just ensure all nodes are primary validators (so can be subnet validators)
	if err := ln.addPrimaryValidators(ctx, platformCli, w); err != nil {
		return nil, err
	}

	subnetIDs, err := createSubnets(ctx, uint32(len(subnetSpecs)), w, ln.log)
	if err != nil {
		return nil, err
	}

	if err := ln.setSubnetConfigFiles(subnetIDs, subnetSpecs); err != nil {
		return nil, err
	}

	if err := ln.restartNodes(ctx, subnetIDs, subnetSpecs); err != nil {
		return nil, err
	}

	// wait for nodes to be primary validators before trying to add them as subnet ones
	if err = ln.waitPrimaryValidators(ctx, platformCli); err != nil {
		return nil, err
	}

	if err = ln.addSubnetValidators(ctx, platformCli, w, subnetIDs, subnetSpecs); err != nil {
		return nil, err
	}

	if err = ln.waitSubnetValidators(ctx, platformCli, subnetIDs, subnetSpecs); err != nil {
		return nil, err
	}

	return subnetIDs, nil
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

	clientURI, err := ln.getClientURI()
	if err != nil {
		return err
	}
	platformCli := platformvm.NewClient(clientURI)

	for _, chainInfo := range chainInfos {
		cctx, cancel := createDefaultCtx(ctx)
		vs, err := platformCli.GetCurrentValidators(cctx, chainInfo.subnetID, nil)
		cancel()
		if err != nil {
			return err
		}
		nodeNames := []string{}
		for _, v := range vs {
			for nodeName, node := range ln.nodes {
				if v.NodeID == node.GetNodeID() {
					nodeNames = append(nodeNames, nodeName)
				}
			}
		}
		if len(nodeNames) != len(vs) {
			return fmt.Errorf("not all validators for subnet %s are present in network", chainInfo.subnetID.String())
		}

		for _, nodeName := range nodeNames {
			node := ln.nodes[nodeName]
			if node.paused {
				continue
			}
			ln.log.Info("inspecting node log directory for custom chain logs", zap.String("log-dir", node.GetLogsDir()), zap.String("node-name", nodeName))
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

func (ln *localNetwork) restartNodes(
	ctx context.Context,
	subnetIDs []ids.ID,
	subnetSpecs []network.SubnetSpec,
) (err error) {
	fmt.Println()
	ln.log.Info(logging.Blue.Wrap(logging.Bold.Wrap("restarting network")))

	nodeNames := maps.Keys(ln.nodes)
	sort.Strings(nodeNames)

	for _, nodeName := range nodeNames {
		node := ln.nodes[nodeName]

		// delete node specific flag so as to use default one
		nodeConfig := node.GetConfig()

		previousTrackedSubnets := ""
		previousTrackedSubnetsIntf, ok := nodeConfig.Flags[config.TrackSubnetsKey]
		if ok {
			previousTrackedSubnets, ok = previousTrackedSubnetsIntf.(string)
			if !ok {
				return fmt.Errorf("expected node config %s to have type string obtained %T", config.TrackSubnetsKey, previousTrackedSubnetsIntf)
			}
		}

		trackSubnetIDsSet := set.Set[string]{}
		if previousTrackedSubnets != "" {
			for _, s := range strings.Split(previousTrackedSubnets, ",") {
				trackSubnetIDsSet.Add(s)
			}
		}
		needsRestart := false
		for i, subnetID := range subnetIDs {
			for _, participant := range subnetSpecs[i].Participants {
				if participant == nodeName {
					trackSubnetIDsSet.Add(subnetID.String())
					needsRestart = true
				}
			}
		}
		trackSubnetIDs := trackSubnetIDsSet.List()
		sort.Strings(trackSubnetIDs)

		tracked := strings.Join(trackSubnetIDs, ",")
		nodeConfig.Flags[config.TrackSubnetsKey] = tracked

		if !needsRestart {
			continue
		}

		if node.paused {
			continue
		}

		ln.log.Info(logging.Green.Wrap(fmt.Sprintf("restarting node %s to track subnets %s", nodeName, tracked)))

		if err := ln.restartNode(ctx, nodeName, "", "", "", nil, nil, nil); err != nil {
			return err
		}

		if err := ln.healthy(ctx); err != nil {
			return err
		}
	}
	return nil
}

type wallet struct {
	addr     ids.ShortID
	pWallet  p.Wallet
	pBackend p.Backend
	pBuilder p.Builder
	pSigner  p.Signer
}

func newWallet(
	ctx context.Context,
	uri string,
	preloadTXs []ids.ID,
) (*wallet, error) {
	kc := secp256k1fx.NewKeychain(genesis.EWOQKey)
	pCTX, _, utxos, err := primary.FetchState(ctx, uri, kc.Addresses())
	if err != nil {
		return nil, err
	}
	pClient := platformvm.NewClient(uri)
	pTXs := make(map[ids.ID]*txs.Tx)
	for _, id := range preloadTXs {
		txBytes, err := pClient.GetTx(ctx, id)
		if err != nil {
			return nil, err
		}
		tx, err := txs.Parse(txs.Codec, txBytes)
		if err != nil {
			return nil, err
		}
		pTXs[id] = tx
	}
	pUTXOs := primary.NewChainUTXOs(constants.PlatformChainID, utxos)
	var w wallet
	w.addr = genesis.EWOQKey.PublicKey().Address()
	w.pBackend = p.NewBackend(pCTX, pUTXOs, pTXs)
	w.pBuilder = p.NewBuilder(kc.Addresses(), w.pBackend)
	w.pSigner = p.NewSigner(kc, w.pBackend)
	w.pWallet = p.NewWallet(w.pBuilder, w.pSigner, pClient, w.pBackend)
	return &w, nil
}

// add all nodes as validators of the primary network, in case they are not
// the validation starts as soon as possible and its duration is as long as possible, that is,
// it is set to max accepted duration by avalanchego
func (ln *localNetwork) addPrimaryValidators(
	ctx context.Context,
	platformCli platformvm.Client,
	w *wallet,
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

		// Prepare node BLS PoP
		// It is important to note that this will ONLY register BLS signers for
		// nodes registered AFTER genesis.
		blsKeyBytes, err := base64.StdEncoding.DecodeString(node.GetConfig().StakingSigningKey)
		if err != nil {
			return err
		}
		blsSk, err := bls.SecretKeyFromBytes(blsKeyBytes)
		if err != nil {
			return err
		}
		proofOfPossession := signer.NewProofOfPossession(blsSk)
		cctx, cancel = createDefaultCtx(ctx)
		txID, err := w.pWallet.IssueAddPermissionlessValidatorTx(
			&txs.SubnetValidator{
				Validator: txs.Validator{
					NodeID: nodeID,
					Start:  uint64(time.Now().Add(validationStartOffset).Unix()),
					End:    uint64(time.Now().Add(validationDuration).Unix()),
					Wght:   genesis.LocalParams.MinValidatorStake,
				},
				Subnet: ids.Empty,
			},
			proofOfPossession,
			w.pWallet.AVAXAssetID(),
			&secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{w.addr},
			},
			&secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{w.addr},
			},
			10*10000, // 10% fee percent, times 10000 to make it as shares
			common.WithContext(cctx),
		)
		cancel()
		if err != nil {
			return fmt.Errorf("P-Wallet Tx Error %s %w, node ID %s", "IssueAddPermissionlessValidatorTx", err, nodeID)
		}
		ln.log.Info("added node as primary subnet validator", zap.String("node-name", nodeName), zap.String("node-ID", nodeID.String()), zap.String("tx-ID", txID.String()))
	}
	return nil
}

func createSubnets(
	ctx context.Context,
	numSubnets uint32,
	w *wallet,
	log logging.Logger,
) ([]ids.ID, error) {
	fmt.Println()
	log.Info(logging.Green.Wrap("creating subnets"), zap.Uint32("num-subnets", numSubnets))
	subnetIDs := make([]ids.ID, numSubnets)
	for i := uint32(0); i < numSubnets; i++ {
		log.Info("creating subnet tx")
		cctx, cancel := createDefaultCtx(ctx)
		subnetID, err := w.pWallet.IssueCreateSubnetTx(
			&secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{w.addr},
			},
			common.WithContext(cctx),
			defaultPoll,
		)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("P-Wallet Tx Error %s %w", "IssueCreateSubnetTx", err)
		}
		log.Info("created subnet tx", zap.String("subnet-ID", subnetID.String()))
		subnetIDs[i] = subnetID
	}
	return subnetIDs, nil
}

// add the nodes in subnet participant as validators of the given subnets, in case they are not
// the validation starts as soon as possible and its duration is as long as possible, that is,
// it ends at the time the primary network validation ends for the node
func (ln *localNetwork) addSubnetValidators(
	ctx context.Context,
	platformCli platformvm.Client,
	w *wallet,
	subnetIDs []ids.ID,
	subnetSpecs []network.SubnetSpec,
) error {
	ln.log.Info(logging.Green.Wrap("adding the nodes as subnet validators"))
	for i, subnetID := range subnetIDs {
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
		participants := subnetSpecs[i].Participants
		for _, nodeName := range participants {
			node, b := ln.nodes[nodeName]
			if !b {
				return fmt.Errorf("participant node %s is not in network nodes", nodeName)
			}
			nodeID := node.GetNodeID()
			if isValidator := subnetValidators.Contains(nodeID); isValidator {
				continue
			}
			cctx, cancel := createDefaultCtx(ctx)
			txID, err := w.pWallet.IssueAddSubnetValidatorTx(
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
				return fmt.Errorf("P-Wallet Tx Error %s %w, node ID %s, subnetID %s", "IssueAddSubnetValidatorTx", err, nodeID, subnetID)
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

// waits until all nodes start validating the primary network
func (ln *localNetwork) waitPrimaryValidators(
	ctx context.Context,
	platformCli platformvm.Client,
) error {
	ln.log.Info(logging.Green.Wrap("waiting for the nodes to become primary validators"))
	for {
		ready := true
		cctx, cancel := createDefaultCtx(ctx)
		vs, err := platformCli.GetCurrentValidators(cctx, constants.PrimaryNetworkID, nil)
		cancel()
		if err != nil {
			return err
		}
		primaryValidators := set.Set[ids.NodeID]{}
		for _, v := range vs {
			primaryValidators.Add(v.NodeID)
		}
		for _, node := range ln.nodes {
			nodeID := node.GetNodeID()
			if isValidator := primaryValidators.Contains(nodeID); !isValidator {
				ready = false
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

// waits until all subnet participants start validating the subnetID, for all given subnets
func (ln *localNetwork) waitSubnetValidators(
	ctx context.Context,
	platformCli platformvm.Client,
	subnetIDs []ids.ID,
	subnetSpecs []network.SubnetSpec,
) error {
	ln.log.Info(logging.Green.Wrap("waiting for the nodes to become subnet validators"))
	for {
		ready := true
		for i, subnetID := range subnetIDs {
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
			participants := subnetSpecs[i].Participants
			for _, nodeName := range participants {
				node, b := ln.nodes[nodeName]
				if !b {
					return fmt.Errorf("participant node %s is not in network nodes", nodeName)
				}
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
	w *wallet,
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
		utx, err := w.pBuilder.NewCreateChainTx(
			subnetID,
			genesisBytes,
			vmID,
			nil,
			vmName,
		)
		if err != nil {
			return nil, fmt.Errorf("failure generating create blockchain tx: %w", err)
		}
		tx, err := w.pSigner.SignUnsigned(cctx, utx)
		if err != nil {
			return nil, fmt.Errorf("failure signing create blockchain tx: %w", err)
		}
		err = w.pBackend.AcceptTx(cctx, tx)
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
	}
	return created
}

func (ln *localNetwork) setSubnetConfigFiles(
	subnetIDs []ids.ID,
	subnetSpecs []network.SubnetSpec,
) error {
	for i, subnetID := range subnetIDs {
		participants := subnetSpecs[i].Participants
		subnetConfig := subnetSpecs[i].SubnetConfig
		if subnetConfig != nil {
			for _, nodeName := range participants {
				_, b := ln.nodes[nodeName]
				if !b {
					return fmt.Errorf("participant node %s is not in network nodes", nodeName)
				}
				ln.nodes[nodeName].config.SubnetConfigFiles[subnetID.String()] = string(subnetConfig)
			}
		}
	}
	return nil
}

func (*localNetwork) createBlockchains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
	blockchainTxs []*txs.Tx,
	w *wallet,
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

		blockchainID, err := w.pWallet.IssueTx(
			blockchainTxs[i],
			common.WithContext(cctx),
			defaultPoll,
		)
		if err != nil {
			return fmt.Errorf("P-Wallet Tx Error %s %w, blockchainID %s", "Issue Create Blockchain Tx", err, blockchainTxs[i])
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
