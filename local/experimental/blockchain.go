package experimental

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/chain/p"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary/common"
	"go.uber.org/zap"
)

const (
	defaultTimeout = time.Minute
	// offset of validation start from current time
	validationStartOffset = 20 * time.Second
	// duration for primary network validators
	validationDuration = 365 * 24 * time.Hour
	// weight assigned to subnet validators
	subnetValidatorsWeight = 1000
)

var defaultPoll = common.WithPollFrequency(100 * time.Millisecond)

type LocalNetwork interface {
	GetClientURIUnsafe() (string, error)
	GetNodeUnsafe(name string) node.Node
	Logger() logging.Logger
	SetBlockchainConfigFilesUnsafe(chainSpecs []network.BlockchainSpec, blockchainTxs []*txs.Tx) bool
	RestartNodeUnsafe(ctx context.Context, nodeName string) error
	HealthyUnsafe(ctx context.Context) error
	ReloadVMPluginsUnsafe(ctx context.Context) error
}

type wallet struct {
	wallet p.Wallet
	addr   ids.ShortID
	log    logging.Logger

	client  platformvm.Client
	backend p.Backend
	builder p.Builder
	signer  p.Signer
}

func newWallet(ctx context.Context, uri string, logger logging.Logger) (*wallet, error) {
	kc := secp256k1fx.NewKeychain(genesis.EWOQKey)
	pCtx, _, utxos, err := primary.FetchState(ctx, uri, kc.Addresses())
	if err != nil {
		return nil, err
	}
	addrs := kc.Addresses()
	pUTXOs := primary.NewChainUTXOs(constants.PlatformChainID, utxos)
	var w wallet
	w.client = platformvm.NewClient(uri)
	w.addr = genesis.EWOQKey.PublicKey().Address()
	w.log = logger
	w.backend = p.NewBackend(pCtx, pUTXOs, map[ids.ID]*txs.Tx{})
	w.builder = p.NewBuilder(addrs, w.backend)
	w.signer = p.NewSigner(kc, w.backend)
	w.wallet = p.NewWallet(w.builder, w.signer, w.client, w.backend)
	return &w, nil
}

func (w *wallet) reload(uri string) {
	w.client = platformvm.NewClient(uri)
	w.wallet = p.NewWallet(w.builder, w.signer, w.client, w.backend)
}

func (w *wallet) createSubnets(
	ctx context.Context,
	numSubnets int,
) ([]ids.ID, error) {
	w.log.Info(logging.Green.Wrap("creating subnets VM"), zap.Int("num-subnets", numSubnets))
	subnetIDs := make([]ids.ID, numSubnets)
	for i := 0; i < numSubnets; i++ {
		w.log.Info("creating subnet tx")
		cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
		subnetID, err := w.wallet.IssueCreateSubnetTx(
			&secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{w.addr},
			},
			common.WithContext(cctx),
			defaultPoll,
		)
		cancel()
		if err != nil {
			return nil, err
		}
		w.log.Info("created subnet tx", zap.String("subnet-ID", subnetID.String()))
		subnetIDs[i] = subnetID
	}
	return subnetIDs, nil
}

func (w *wallet) addPrimaryValidators(
	ctx context.Context,
	ln LocalNetwork,
	names map[string]struct{},
) error {
	w.log.Info(logging.Green.Wrap("adding the nodes as primary network validators"))
	// ref. https://docs.avax.network/build/avalanchego-apis/p-chain/#platformgetcurrentvalidators
	cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	vs, err := w.client.GetCurrentValidators(cctx, constants.PrimaryNetworkID, nil)
	cancel()
	if err != nil {
		return err
	}
	curValidators := make(map[ids.NodeID]struct{})
	for _, v := range vs {
		curValidators[v.NodeID] = struct{}{}
	}
	addedValidators := []ids.NodeID{}
	for nodeName := range names {
		// TODO: handle missing name
		node := ln.GetNodeUnsafe(nodeName)
		nodeID := node.GetNodeID()

		_, isValidator := curValidators[nodeID]
		if isValidator {
			continue
		}

		// Prepare node BLS PoP
		//
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

		cctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		txID, err := w.wallet.IssueAddPermissionlessValidatorTx(
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
			w.wallet.AVAXAssetID(),
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
			return err
		}
		w.log.Info("added node as primary subnet validator", zap.String("node-name", nodeName), zap.String("node-ID", nodeID.String()), zap.String("tx-ID", txID.String()))
		addedValidators = append(addedValidators, nodeID)
	}

	// Ensure all added nodes are primary validators (else add subnet validator will revert)
	for ctx.Err() == nil {
		vs, err := w.client.GetCurrentValidators(ctx, ids.Empty, nil)
		if err != nil {
			return err
		}
		curValidators := make(map[ids.NodeID]struct{})
		for _, v := range vs {
			curValidators[v.NodeID] = struct{}{}
		}
		ready := true
		for _, nodeID := range addedValidators {
			if _, ok := curValidators[nodeID]; !ok {
				ready = false
			}
		}
		if ready {
			break
		}
		time.Sleep(5 * time.Second)
		w.log.Info("waiting for all validators to become active")
	}
	return ctx.Err()
}

func (w *wallet) createBlockchainTxs(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
) ([]*txs.Tx, error) {
	w.log.Info(logging.Green.Wrap("creating tx for each custom chain"))
	blockchainTxs := make([]*txs.Tx, len(chainSpecs))
	for i, chainSpec := range chainSpecs {
		vmName := chainSpec.VMName
		vmID, err := utils.VMID(vmName)
		if err != nil {
			return nil, err
		}
		genesisBytes := chainSpec.Genesis

		w.log.Info("creating blockchain tx",
			zap.String("vm-name", vmName),
			zap.String("vm-ID", vmID.String()),
			zap.Int("bytes length of genesis", len(genesisBytes)),
		)
		cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
		subnetID, err := ids.FromString(*chainSpec.SubnetID)
		if err != nil {
			return nil, err
		}
		utx, err := w.wallet.Builder().NewCreateChainTx(
			subnetID,
			genesisBytes,
			vmID,
			nil,
			vmName,
		)
		if err != nil {
			return nil, fmt.Errorf("failure generating create blockchain tx: %w", err)
		}
		tx, err := w.wallet.Signer().SignUnsigned(cctx, utx)
		if err != nil {
			return nil, fmt.Errorf("failure signing create blockchain tx: %w", err)
		}
		blockchainTxs[i] = tx
	}

	return blockchainTxs, nil
}

func restartNodes(
	ctx context.Context,
	ln LocalNetwork,
	chainSpecs []network.BlockchainSpec,
) error {
	for _, spec := range chainSpecs {
		trackSubnetID := *spec.SubnetID
		for _, nodeName := range spec.Participants {
			ln.Logger().Info("restarting validator", zap.String("nodeName", nodeName), zap.String("track-subnets", trackSubnetID))
			node := ln.GetNodeUnsafe(nodeName)
			nodeConfig := node.GetConfig()
			nodeConfig.Flags[config.TrackSubnetsKey] = trackSubnetID
			if err := ln.RestartNodeUnsafe(ctx, nodeName); err != nil {
				return err
			}
			if err := ln.HealthyUnsafe(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *wallet) createBlockchains(
	ctx context.Context,
	chainSpecs []network.BlockchainSpec,
	blockchainTxs []*txs.Tx,
) error {
	w.log.Info(logging.Green.Wrap("creating each custom chain"))
	for i, chainSpec := range chainSpecs {
		vmName := chainSpec.VMName
		vmID, err := utils.VMID(vmName)
		if err != nil {
			return err
		}
		w.log.Info("creating blockchain",
			zap.String("vm-name", vmName),
			zap.String("vm-ID", vmID.String()),
		)

		cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
		defer cancel()

		blockchainID, err := w.wallet.IssueTx(
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

		w.log.Info("created a new blockchain",
			zap.String("vm-name", vmName),
			zap.String("vm-ID", vmID.String()),
			zap.String("blockchain-ID", blockchainID.String()),
		)
	}
	return nil
}

func (w *wallet) addSubnetValidators(
	ctx context.Context,
	ln LocalNetwork,
	chainSpecs []network.BlockchainSpec,
) error {
	w.log.Info(logging.Green.Wrap("adding the nodes as subnet validators"))
	cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	vs, err := w.client.GetCurrentValidators(cctx, constants.PrimaryNetworkID, nil)
	cancel()
	if err != nil {
		return err
	}
	primaryValidatorsEndtime := make(map[ids.NodeID]time.Time)
	for _, v := range vs {
		primaryValidatorsEndtime[v.NodeID] = time.Unix(int64(v.EndTime), 0)
	}
	for _, spec := range chainSpecs {
		subnetID, err := ids.FromString(*spec.SubnetID)
		if err != nil {
			return err
		}
		nodeIDs := make([]ids.NodeID, len(spec.Participants))
		for i, nodeName := range spec.Participants {
			node := ln.GetNodeUnsafe(nodeName)
			nodeID := node.GetNodeID()
			nodeIDs[i] = nodeID
			cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
			txID, err := w.wallet.IssueAddSubnetValidatorTx(
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
			w.log.Info("added node as a subnet validator to subnet",
				zap.String("node-name", nodeName),
				zap.String("node-ID", nodeID.String()),
				zap.String("subnet-ID", subnetID.String()),
				zap.String("tx-ID", txID.String()),
			)
		}

		// Wait for validators to become active
		for ctx.Err() == nil {
			vs, err := w.client.GetCurrentValidators(ctx, subnetID, nil)
			if err != nil {
				return err
			}
			curValidators := make(map[ids.NodeID]struct{})
			for _, v := range vs {
				curValidators[v.NodeID] = struct{}{}
			}
			ready := true
			for _, nodeID := range nodeIDs {
				if _, ok := curValidators[nodeID]; !ok {
					ready = false
				}
			}
			if ready {
				break
			}
			time.Sleep(5 * time.Second)
			w.log.Info("waiting for all subnet validators to become active")
		}
	}
	return nil
}

func CreateSpecificBlockchains(
	ctx context.Context,
	ln LocalNetwork,
	chainSpecs []network.BlockchainSpec, // VM name + genesis bytes
) ([]network.BlockchainSpec, []ids.ID, error) {
	clientURI, err := ln.GetClientURIUnsafe()
	if err != nil {
		return nil, nil, err
	}
	wallet, err := newWallet(ctx, clientURI, ln.Logger())
	if err != nil {
		return nil, nil, err
	}

	// Ensure all participants are validators
	allNames := map[string]struct{}{}
	for _, spec := range chainSpecs {
		for _, name := range spec.Participants {
			allNames[name] = struct{}{}
		}
	}
	if err := wallet.addPrimaryValidators(ctx, ln, allNames); err != nil {
		return nil, nil, err
	}

	// Create new subnets
	subnets, err := wallet.createSubnets(ctx, len(chainSpecs))
	if err != nil {
		return nil, nil, err
	}
	for i, subnet := range subnets {
		subnetIDStr := subnet.String()
		chainSpecs[i].SubnetID = &subnetIDStr
	}

	// Restart validators
	if err := restartNodes(ctx, ln, chainSpecs); err != nil {
		return nil, nil, err
	}
	clientURI, err = ln.GetClientURIUnsafe()
	if err != nil {
		return nil, nil, err
	}
	wallet.reload(clientURI)

	// Add subnet validators
	if err := wallet.addSubnetValidators(ctx, ln, chainSpecs); err != nil {
		return nil, nil, err
	}

	// Create and store chain configs
	blockchainTxs, err := wallet.createBlockchainTxs(ctx, chainSpecs)
	if err != nil {
		return nil, nil, err
	}
	chainIDs := make([]ids.ID, len(blockchainTxs))
	for i, tx := range blockchainTxs {
		chainIDs[i] = tx.ID()
	}
	ln.SetBlockchainConfigFilesUnsafe(chainSpecs, blockchainTxs)

	// Restart validators
	if err := restartNodes(ctx, ln, chainSpecs); err != nil {
		return nil, nil, err
	}
	clientURI, err = ln.GetClientURIUnsafe()
	if err != nil {
		return nil, nil, err
	}
	wallet.reload(clientURI)

	// Reload plugins and create blockchains
	if err := ln.ReloadVMPluginsUnsafe(ctx); err != nil {
		return nil, nil, err
	}
	if err := wallet.createBlockchains(ctx, chainSpecs, blockchainTxs); err != nil {
		return nil, nil, err
	}
	return chainSpecs, chainIDs, nil
}
