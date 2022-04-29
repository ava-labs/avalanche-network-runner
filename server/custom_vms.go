// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ava-labs/avalanche-network-runner/pkg/color"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary/common"
	"go.uber.org/zap"
)

// provisions local cluster and install custom VMs if applicable
// assumes the local cluster is already set up and healthy
func (lc *localNetwork) installCustomVMs(ctx context.Context) error {
	println()
	color.Outf("{{blue}}{{bold}}create and install custom VMs{{/}}\n")

	uris := make([]string, 0, len(lc.nodeInfos))
	for _, i := range lc.nodeInfos {
		uris = append(uris, i.Uri)
	}
	httpRPCEp := uris[0]
	platformCli := platformvm.NewClient(httpRPCEp)

	baseWallet, avaxAssetID, testKeyAddr, err := lc.setupWallet(ctx, httpRPCEp)
	if err != nil {
		return err
	}
	validatorIDs, err := lc.checkValidators(ctx, platformCli, baseWallet, testKeyAddr)
	if err != nil {
		return err
	}
	if err = lc.createSubnets(ctx, baseWallet, testKeyAddr); err != nil {
		return err
	}
	if err = lc.restartNodesWithWhitelistedSubnets(ctx); err != nil {
		return err
	}

	println()
	color.Outf("{{green}}refreshing the wallet with the new URIs after restarts{{/}}\n")
	uris = make([]string, 0, len(lc.nodeInfos))
	for _, i := range lc.nodeInfos {
		uris = append(uris, i.Uri)
	}
	httpRPCEp = uris[0]
	baseWallet.refresh(httpRPCEp)
	zap.L().Info("set up base wallet with pre-funded test key",
		zap.String("http-rpc-endpoint", httpRPCEp),
		zap.String("address", testKeyAddr.String()),
	)

	if err = lc.addSubnetValidators(ctx, baseWallet, validatorIDs); err != nil {
		return err
	}
	if err = lc.createBlockchains(ctx, baseWallet, testKeyAddr); err != nil {
		return err
	}

	println()
	color.Outf("{{green}}checking the remaining balance of the base wallet{{/}}\n")
	balances, err := baseWallet.P().Builder().GetBalance()
	if err != nil {
		return err
	}
	zap.L().Info("base wallet AVAX balance",
		zap.String("address", testKeyAddr.String()),
		zap.Uint64("balance", balances[avaxAssetID]),
	)

	return nil
}

func (lc *localNetwork) waitForCustomVMsReady(ctx context.Context) error {
	println()
	color.Outf("{{blue}}{{bold}}waiting for custom VMs to report healthy...{{/}}\n")

	hc := lc.nw.Healthy(ctx)
	select {
	case <-lc.stopc:
		return errAborted
	case <-ctx.Done():
		return ctx.Err()
	case err := <-hc:
		if err != nil {
			return err
		}
	}

	for nodeName, nodeInfo := range lc.nodeInfos {
		zap.L().Info("inspecting node log directory for custom VM logs",
			zap.String("node-name", nodeName),
			zap.String("log-dir", nodeInfo.LogDir),
		)
		for _, vmInfo := range lc.customVMIDToInfo {
			p := filepath.Join(nodeInfo.LogDir, vmInfo.info.BlockchainId+".log")
			zap.L().Info("checking log",
				zap.String("vm-id", vmInfo.info.VmId),
				zap.String("subnet-id", vmInfo.info.SubnetId),
				zap.String("blockchain-id", vmInfo.info.BlockchainId),
				zap.String("log-path", p),
			)
			for {
				_, err := os.Stat(p)
				if err == nil {
					zap.L().Info("found the log", zap.String("log-path", p))
					break
				}

				zap.L().Info("log not found yet, retrying...",
					zap.String("vm-id", vmInfo.info.VmId),
					zap.String("subnet-id", vmInfo.info.SubnetId),
					zap.String("blockchain-id", vmInfo.info.BlockchainId),
					zap.String("log-path", p),
					zap.Error(err),
				)
				select {
				case <-lc.stopc:
					return errAborted
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(10 * time.Second):
				}
			}
		}
	}

	println()
	color.Outf("{{green}}{{bold}}all custom VMs are running!!!{{/}}\n")
	for _, i := range lc.nodeInfos {
		for vmID, vmInfo := range lc.customVMIDToInfo {
			color.Outf("{{blue}}{{bold}}[blockchain RPC for %q] \"%s/ext/bc/%s\"{{/}}\n", vmID, i.GetUri(), vmInfo.blockchainID.String())
		}
	}

	lc.customVMsReadycCloseOnce.Do(func() {
		println()
		color.Outf("{{green}}{{bold}}all custom VMs are ready!!!{{/}}\n")
		close(lc.customVMsReadyc)
	})
	return nil
}

func (lc *localNetwork) setupWallet(ctx context.Context, httpRPCEp string) (baseWallet *refreshableWallet, avaxAssetID ids.ID, testKeyAddr ids.ShortID, err error) {
	// "local/default/genesis.json" pre-funds "ewoq" key
	testKey := genesis.EWOQKey
	testKeyAddr = testKey.PublicKey().Address()
	testKeychain := secp256k1fx.NewKeychain(genesis.EWOQKey)

	println()
	color.Outf("{{green}}setting up the base wallet with the seed test key{{/}}\n")
	baseWallet, err = createRefreshableWallet(ctx, httpRPCEp, testKeychain)
	if err != nil {
		return nil, ids.Empty, ids.ShortEmpty, err
	}
	zap.L().Info("set up base wallet with pre-funded test key",
		zap.String("http-rpc-endpoint", httpRPCEp),
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
		zap.String("http-rpc-endpoint", httpRPCEp),
		zap.String("address", testKeyAddr.String()),
		zap.Uint64("balance", bal),
	)

	return baseWallet, avaxAssetID, testKeyAddr, nil
}

func (lc *localNetwork) checkValidators(ctx context.Context, platformCli platformvm.Client, baseWallet *refreshableWallet, testKeyAddr ids.ShortID) (validatorIDs []ids.NodeID, err error) {
	println()
	color.Outf("{{green}}fetching all nodes from the existing cluster to make sure all nodes are validating the primary network/subnet{{/}}\n")
	// ref. https://docs.avax.network/build/avalanchego-apis/p-chain/#platformgetcurrentvalidators
	cctx, cancel := createDefaultCtx(ctx)
	vs, err := platformCli.GetCurrentValidators(cctx, constants.PrimaryNetworkID, nil)
	cancel()
	if err != nil {
		return nil, err
	}
	curValidators := make(map[ids.NodeID]struct{})
	for _, v := range vs {
		curValidators[v.NodeID] = struct{}{}
		zap.L().Info("current validator", zap.String("node-id", v.NodeID.String()))
	}

	println()
	color.Outf("{{green}}adding all nodes as validator for the primary subnet{{/}}\n")
	validatorIDs = make([]ids.NodeID, 0, len(lc.nodeInfos))
	for nodeName, nodeInfo := range lc.nodeInfos {
		nodeID, err := ids.NodeIDFromString(nodeInfo.Id)
		if err != nil {
			return nil, err
		}
		validatorIDs = append(validatorIDs, nodeID)

		_, isValidator := curValidators[nodeID]
		if isValidator {
			zap.L().Info("the node is already validating the primary subnet; skipping",
				zap.String("node-name", nodeName),
				zap.String("node-id", nodeInfo.Id),
			)
			continue
		}

		zap.L().Info("adding a node as a validator to the primary subnet",
			zap.String("node-name", nodeName),
			zap.String("node-id", nodeID.String()),
		)
		cctx, cancel = createDefaultCtx(ctx)
		txID, err := baseWallet.P().IssueAddValidatorTx(
			&platformvm.Validator{
				NodeID: nodeID,
				Start:  uint64(time.Now().Add(10 * time.Second).Unix()),
				End:    uint64(time.Now().Add(300 * time.Hour).Unix()),
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
			return nil, err
		}
		zap.L().Info("added the node as primary subnet validator",
			zap.String("node-name", nodeName),
			zap.String("node-id", nodeInfo.Id),
			zap.String("tx-id", txID.String()),
		)
	}
	return validatorIDs, nil
}

func (lc *localNetwork) createSubnets(ctx context.Context, baseWallet *refreshableWallet, testKeyAddr ids.ShortID) error {
	println()
	color.Outf("{{green}}creating subnet for each custom VM{{/}}\n")
	for vmName := range lc.customVMNameToGenesis {
		vmID, err := utils.VMID(vmName)
		if err != nil {
			return err
		}
		zap.L().Info("creating subnet tx",
			zap.String("vm-name", vmName),
			zap.String("vm-id", vmID.String()),
		)
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
			return err
		}
		zap.L().Info("created subnet tx",
			zap.String("vm-name", vmName),
			zap.String("vm-id", vmID.String()),
			zap.String("subnet-id", subnetID.String()),
		)
		lc.customVMIDToInfo[vmID] = vmInfo{
			info: &rpcpb.CustomVmInfo{
				VmName:       vmName,
				VmId:         vmID.String(),
				SubnetId:     subnetID.String(),
				BlockchainId: "",
			},
			subnetID: subnetID,
		}
	}
	return nil
}

// TODO: make this "restart" pattern more generic, so it can be used for "Restart" RPC
func (lc *localNetwork) restartNodesWithWhitelistedSubnets(ctx context.Context) (err error) {
	println()
	color.Outf("{{green}}restarting each node with --whitelisted-subnets{{/}}\n")
	whitelistedSubnetIDs := make([]string, 0, len(lc.customVMIDToInfo))
	for _, vmInfo := range lc.customVMIDToInfo {
		whitelistedSubnetIDs = append(whitelistedSubnetIDs, vmInfo.subnetID.String())
	}
	sort.Strings(whitelistedSubnetIDs)
	whitelistedSubnets := strings.Join(whitelistedSubnetIDs, ",")
	for nodeName, v := range lc.nodeInfos {
		zap.L().Info("updating node info",
			zap.String("node-name", nodeName),
			zap.String("whitelisted-subnets", whitelistedSubnets),
		)
		v.WhitelistedSubnets = whitelistedSubnets
		lc.nodeInfos[nodeName] = v
	}
	for i := range lc.cfg.NodeConfigs {
		nodeName := lc.cfg.NodeConfigs[i].Name

		zap.L().Info("updating node config and info",
			zap.String("node-name", nodeName),
			zap.String("whitelisted-subnets", whitelistedSubnets),
		)

		// replace "whitelisted-subnets" flag
		lc.cfg.NodeConfigs[i].ConfigFile, err = utils.UpdateJSONKey(lc.cfg.NodeConfigs[i].ConfigFile, "whitelisted-subnets", whitelistedSubnets)
		if err != nil {
			return err
		}

		v := lc.nodeInfos[nodeName]
		v.Config = []byte(lc.cfg.NodeConfigs[i].ConfigFile)
		lc.nodeInfos[nodeName] = v
	}
	zap.L().Info("restarting all nodes to whitelist subnet",
		zap.Strings("whitelisted-subnets", whitelistedSubnetIDs),
	)
	for _, nodeConfig := range lc.cfg.NodeConfigs {
		nodeName := nodeConfig.Name

		lc.customVMRestartMu.Lock()
		zap.L().Info("removing and adding back the node for whitelisted subnets", zap.String("node-name", nodeName))
		if err := lc.nw.RemoveNode(nodeName); err != nil {
			lc.customVMRestartMu.Unlock()
			return err
		}
		if _, err := lc.nw.AddNode(nodeConfig); err != nil {
			lc.customVMRestartMu.Unlock()
			return err
		}

		zap.L().Info("waiting for local cluster readiness after restart", zap.String("node-name", nodeName))
		if err := lc.waitForLocalClusterReady(ctx); err != nil {
			lc.customVMRestartMu.Unlock()
			return err
		}
		lc.customVMRestartMu.Unlock()
	}
	return nil
}

func (lc *localNetwork) addSubnetValidators(ctx context.Context, baseWallet *refreshableWallet, validatorIDs []ids.NodeID) error {
	println()
	color.Outf("{{green}}adding all nodes as subnet validator for each subnet{{/}}\n")
	for vmID, vmInfo := range lc.customVMIDToInfo {
		zap.L().Info("adding all nodes as subnet validator",
			zap.String("vm-name", vmInfo.info.VmName),
			zap.String("vm-id", vmID.String()),
			zap.String("subnet-id", vmInfo.subnetID.String()),
		)
		for _, validatorID := range validatorIDs {
			cctx, cancel := createDefaultCtx(ctx)
			txID, err := baseWallet.P().IssueAddSubnetValidatorTx(
				&platformvm.SubnetValidator{
					Validator: platformvm.Validator{
						NodeID: validatorID,

						// reasonable delay in most/slow test environments
						Start: uint64(time.Now().Add(time.Minute).Unix()),
						End:   uint64(time.Now().Add(100 * time.Hour).Unix()),
						Wght:  1000,
					},
					Subnet: vmInfo.subnetID,
				},
				common.WithContext(cctx),
				defaultPoll,
			)
			cancel()
			if err != nil {
				return err
			}
			zap.L().Info("added the node as a subnet validator",
				zap.String("vm-name", vmInfo.info.VmName),
				zap.String("vm-id", vmID.String()),
				zap.String("subnet-id", vmInfo.subnetID.String()),
				zap.String("node-id", validatorID.String()),
				zap.String("tx-id", txID.String()),
			)
		}
	}
	return nil
}

func (lc *localNetwork) createBlockchains(ctx context.Context, baseWallet *refreshableWallet, testKeyAddr ids.ShortID) error {
	println()
	color.Outf("{{green}}creating blockchain for each custom VM{{/}}\n")
	for vmID, vmInfo := range lc.customVMIDToInfo {
		vmName := vmInfo.info.VmName
		vmGenesisBytes := lc.customVMNameToGenesis[vmName]

		zap.L().Info("creating blockchain tx",
			zap.String("vm-name", vmName),
			zap.String("vm-id", vmID.String()),
			zap.Int("genesis-bytes", len(vmGenesisBytes)),
		)
		cctx, cancel := createDefaultCtx(ctx)
		blockchainID, err := baseWallet.P().IssueCreateChainTx(
			vmInfo.subnetID,
			vmGenesisBytes,
			vmID,
			nil,
			vmName,
			common.WithContext(cctx),
			defaultPoll,
		)
		cancel()
		if err != nil {
			return err
		}

		vmInfo.info.BlockchainId = blockchainID.String()
		vmInfo.blockchainID = blockchainID
		lc.customVMIDToInfo[vmID] = vmInfo

		zap.L().Info("created a new blockchain",
			zap.String("vm-name", vmName),
			zap.String("vm-id", vmID.String()),
			zap.String("blockchain-id", blockchainID.String()),
		)
	}
	return nil
}

var defaultPoll = common.WithPollFrequency(5 * time.Second)
