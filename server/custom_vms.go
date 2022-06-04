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
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/validator"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary/common"
	"go.uber.org/zap"
)

type blockchainSpec struct {
	vmName   string
	genesis  []byte
	subnetId *string
}

// provisions local cluster and install custom VMs if applicable
// assumes the local cluster is already set up and healthy
func (lc *localNetwork) installCustomVMs(
	ctx context.Context,
	chainSpecs []blockchainSpec,
) ([]vmInfo, error) {
	println()
	color.Outf("{{blue}}{{bold}}create and install custom VMs{{/}}\n")

	httpRPCEp := lc.nodeInfos[lc.nodeNames[0]].Uri
	platformCli := platformvm.NewClient(httpRPCEp)

	// wallet needs txs for all previously created subnets
	pChainCodec, err := createPChainCodec()
	if err != nil {
		return nil, err
	}
	pTXs := make(map[ids.ID]*platformvm.Tx)
	for _, chainSpec := range chainSpecs {
		if chainSpec.subnetId != nil {
			subnetID, err := ids.FromString(*chainSpec.subnetId)
			if err != nil {
				return nil, err
			}
			subnetTxBytes, err := platformCli.GetTx(ctx, subnetID)
			if err != nil {
				return nil, fmt.Errorf("tx not found for subnet %q: %w", subnetID.String(), err)
			}
			var subnetTx platformvm.Tx
			if _, err := pChainCodec.Unmarshal(subnetTxBytes, &subnetTx); err != nil {
				return nil, fmt.Errorf("couldn not unmarshall tx for subnet %q: %w", subnetID.String(), err)
			}
			pTXs[subnetID] = &subnetTx
		}
	}

	baseWallet, avaxAssetID, testKeyAddr, err := setupWallet(ctx, httpRPCEp, pTXs)
	if err != nil {
		return nil, err
	}
	validatorIDs, err := checkValidators(ctx, lc.nodeInfos, platformCli, baseWallet, testKeyAddr)
	if err != nil {
		return nil, err
	}
	chainInfos, subnetCreated, err := createSubnets(ctx, chainSpecs, baseWallet, testKeyAddr)
	if err != nil {
		return nil, err
	}
	if subnetCreated {
		if err = lc.restartNodesWithWhitelistedSubnets(ctx, chainInfos); err != nil {
			return nil, err
		}
		println()
		color.Outf("{{green}}refreshing the wallet with the new URIs after restarts{{/}}\n")
		httpRPCEp = lc.nodeInfos[lc.nodeNames[0]].Uri
		baseWallet.refresh(httpRPCEp)
		zap.L().Info("set up base wallet with pre-funded test key",
			zap.String("http-rpc-endpoint", httpRPCEp),
			zap.String("address", testKeyAddr.String()),
		)
		if err = addSubnetValidators(ctx, chainSpecs, chainInfos, baseWallet, validatorIDs); err != nil {
			return nil, err
		}
	}

	chainInfos, err = createBlockchains(ctx, chainSpecs, chainInfos, baseWallet, testKeyAddr)
	if err != nil {
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

	return chainInfos, nil
}

func (lc *localNetwork) waitForCustomVMsReady(
	ctx context.Context,
	chainInfos []vmInfo,
) error {
	println()
	color.Outf("{{blue}}{{bold}}waiting for custom VMs to report healthy...{{/}}\n")

	if err := lc.nw.Healthy(ctx); err != nil {
		return err
	}

	for nodeName, nodeInfo := range lc.nodeInfos {
		zap.L().Info("inspecting node log directory for custom VM logs",
			zap.String("node-name", nodeName),
			zap.String("log-dir", nodeInfo.LogDir),
		)
		for _, vmInfo := range chainInfos {
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
				case <-lc.stopCh:
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

	println()
	color.Outf("{{green}}{{bold}}all custom VMs are ready on RPC server-side -- network-runner RPC client can poll and query the cluster status{{/}}\n")

	return nil
}

func setupWallet(
	ctx context.Context,
	httpRPCEp string,
	pTXs map[ids.ID]*platformvm.Tx,
) (baseWallet *refreshableWallet, avaxAssetID ids.ID, testKeyAddr ids.ShortID, err error) {
	// "local/default/genesis.json" pre-funds "ewoq" key
	testKey := genesis.EWOQKey
	testKeyAddr = testKey.PublicKey().Address()
	testKeychain := secp256k1fx.NewKeychain(genesis.EWOQKey)

	println()
	color.Outf("{{green}}setting up the base wallet with the seed test key{{/}}\n")
	baseWallet, err = createRefreshableWallet(ctx, httpRPCEp, testKeychain, pTXs)
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

func checkValidators(
	ctx context.Context,
	nodeInfos map[string]*rpcpb.NodeInfo,
	platformCli platformvm.Client,
	baseWallet *refreshableWallet,
	testKeyAddr ids.ShortID,
) (validatorIDs []ids.NodeID, err error) {
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
	validatorIDs = make([]ids.NodeID, 0, len(nodeInfos))
	for nodeName, nodeInfo := range nodeInfos {
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
			&validator.Validator{
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

func createSubnets(
	ctx context.Context,
	chainSpecs []blockchainSpec,
	baseWallet *refreshableWallet,
	testKeyAddr ids.ShortID,
) ([]vmInfo, bool, error) {
	println()
	color.Outf("{{green}}creating subnet for each custom VM{{/}}\n")
	chainInfos := make([]vmInfo, len(chainSpecs))
	subnetCreated := false
	for i, chainSpec := range chainSpecs {
		vmID, err := utils.VMID(chainSpec.vmName)
		if err != nil {
			return nil, false, err
		}
		if chainSpec.subnetId != nil {
			subnetID, err := ids.FromString(*chainSpec.subnetId)
			if err != nil {
				return nil, false, err
			}
			zap.L().Info("using subnet",
				zap.String("vm-name", chainSpec.vmName),
				zap.String("vm-id", vmID.String()),
				zap.String("subnet-id", subnetID.String()),
			)
			chainInfos[i] = vmInfo{
				info: &rpcpb.CustomVmInfo{
					VmName:       chainSpec.vmName,
					VmId:         vmID.String(),
					SubnetId:     subnetID.String(),
					BlockchainId: "",
				},
				subnetID: subnetID,
			}
		} else {
			zap.L().Info("creating subnet tx",
				zap.String("vm-name", chainSpec.vmName),
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
				return nil, false, err
			}
			zap.L().Info("created subnet tx",
				zap.String("vm-name", chainSpec.vmName),
				zap.String("vm-id", vmID.String()),
				zap.String("subnet-id", subnetID.String()),
			)
			chainInfos[i] = vmInfo{
				info: &rpcpb.CustomVmInfo{
					VmName:       chainSpec.vmName,
					VmId:         vmID.String(),
					SubnetId:     subnetID.String(),
					BlockchainId: "",
				},
				subnetID: subnetID,
			}
			subnetCreated = true
		}
	}
	return chainInfos, subnetCreated, nil
}

// TODO: make this "restart" pattern more generic, so it can be used for "Restart" RPC
func (lc *localNetwork) restartNodesWithWhitelistedSubnets(
	ctx context.Context,
	chainInfos []vmInfo,
) (err error) {
	println()
	color.Outf("{{green}}restarting each node with %s{{/}}\n", config.WhitelistedSubnetsKey)
	whitelistedSubnetIDsMap := map[string]struct{}{}
	for _, vmInfo := range lc.customVMBlockchainIDToInfo {
		whitelistedSubnetIDsMap[vmInfo.subnetID.String()] = struct{}{}
	}
	for _, vmInfo := range chainInfos {
		whitelistedSubnetIDsMap[vmInfo.subnetID.String()] = struct{}{}
	}
	whitelistedSubnetIDs := []string{}
	for subnetID := range whitelistedSubnetIDsMap {
		whitelistedSubnetIDs = append(whitelistedSubnetIDs, subnetID)
	}
	sort.Strings(whitelistedSubnetIDs)
	whitelistedSubnets := strings.Join(whitelistedSubnetIDs, ",")
	for i := range lc.cfg.NodeConfigs {
		nodeName := lc.cfg.NodeConfigs[i].Name

		zap.L().Info("updating node config",
			zap.String("node-name", nodeName),
			zap.String("whitelisted-subnets", whitelistedSubnets),
		)

		// replace WhitelistedSubnetsKey flag
		lc.cfg.NodeConfigs[i].ConfigFile, err = utils.SetJSONKey(lc.cfg.NodeConfigs[i].ConfigFile, config.WhitelistedSubnetsKey, whitelistedSubnets)
		if err != nil {
			return err
		}
	}
	zap.L().Info("restarting all nodes to whitelist subnet",
		zap.Strings("whitelisted-subnets", whitelistedSubnetIDs),
	)
	for _, nodeConfig := range lc.cfg.NodeConfigs {
		nodeName := nodeConfig.Name
		lc.customVMRestartMu.Lock()
		zap.L().Info("removing the node", zap.String("node-name", nodeName))
		if err := lc.nw.RemoveNode(nodeName); err != nil {
			lc.customVMRestartMu.Unlock()
			return err
		}
		lc.customVMRestartMu.Unlock()
	}
	for _, nodeConfig := range lc.cfg.NodeConfigs {
		nodeName := nodeConfig.Name
		lc.customVMRestartMu.Lock()
		zap.L().Info("adding back the node", zap.String("node-name", nodeName))
		if _, err := lc.nw.AddNode(nodeConfig); err != nil {
			lc.customVMRestartMu.Unlock()
			return err
		}
		lc.customVMRestartMu.Unlock()
	}
	zap.L().Info("waiting for local cluster readiness after restart")
	if err := lc.waitForLocalClusterReady(ctx); err != nil {
		lc.customVMRestartMu.Unlock()
		return err
	}
	return nil
}

func addSubnetValidators(
	ctx context.Context,
	chainSpecs []blockchainSpec,
	chainInfos []vmInfo,
	baseWallet *refreshableWallet,
	validatorIDs []ids.NodeID,
) error {
	println()
	color.Outf("{{green}}adding all nodes as subnet validator for each subnet{{/}}\n")
	for i, vmInfo := range chainInfos {
		if chainSpecs[i].subnetId != nil {
			continue
		}
		zap.L().Info("adding all nodes as subnet validator",
			zap.String("vm-name", vmInfo.info.VmName),
			zap.String("vm-id", vmInfo.info.VmId),
			zap.String("subnet-id", vmInfo.subnetID.String()),
		)
		for _, validatorID := range validatorIDs {
			cctx, cancel := createDefaultCtx(ctx)
			txID, err := baseWallet.P().IssueAddSubnetValidatorTx(
				&validator.SubnetValidator{
					Validator: validator.Validator{
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
				zap.String("vm-id", vmInfo.info.VmId),
				zap.String("subnet-id", vmInfo.subnetID.String()),
				zap.String("node-id", validatorID.String()),
				zap.String("tx-id", txID.String()),
			)
		}
	}
	return nil
}

func createBlockchains(
	ctx context.Context,
	chainSpecs []blockchainSpec,
	chainInfos []vmInfo,
	baseWallet *refreshableWallet,
	testKeyAddr ids.ShortID,
) ([]vmInfo, error) {
	println()
	color.Outf("{{green}}creating blockchain for each custom VM{{/}}\n")
	updatedChainInfos := make([]vmInfo, len(chainInfos))
	for i, vmInfo := range chainInfos {
		vmName := vmInfo.info.VmName
		vmGenesisBytes := chainSpecs[i].genesis

		zap.L().Info("creating blockchain tx",
			zap.String("vm-name", vmName),
			zap.String("vm-id", vmInfo.info.VmId),
			zap.Int("genesis-bytes", len(vmGenesisBytes)),
		)
		cctx, cancel := createDefaultCtx(ctx)
		vmID, err := ids.FromString(vmInfo.info.VmId)
		if err != nil {
			return nil, err
		}
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
			return nil, fmt.Errorf("failure creating blockchain: %w", err)
		}

		updatedChainInfos[i] = vmInfo
		updatedChainInfos[i].info.BlockchainId = blockchainID.String()
		updatedChainInfos[i].blockchainID = blockchainID

		zap.L().Info("created a new blockchain",
			zap.String("vm-name", vmName),
			zap.String("vm-id", vmInfo.info.VmId),
			zap.String("blockchain-id", blockchainID.String()),
		)
	}

	return updatedChainInfos, nil
}

var defaultPoll = common.WithPollFrequency(100 * time.Millisecond)
