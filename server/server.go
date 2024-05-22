// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// Package server implements server.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/multierr"

	"github.com/ava-labs/avalanche-network-runner/local"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/snow/networking/router"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	// RPCVersion should be bumped anytime changes are made which require
	// the RPC client to upgrade to latest RPC server to be compatible
	RPCVersion   uint32 = 1
	MinNodes     uint32 = 1
	DefaultNodes uint32 = 5

	stopTimeout           = 30 * time.Second
	defaultStartTimeout   = 5 * time.Minute
	waitForHealthyTimeout = 5 * time.Minute

	networkRootDirPrefix   = "network"
	TimeParseLayout        = "2006-01-02 15:04:05"
	StakingMinimumLeadTime = 25 * time.Second
)

var (
	ErrInvalidVMName          = errors.New("invalid VM name")
	ErrInvalidPort            = errors.New("invalid port")
	ErrNotEnoughNodesForStart = errors.New("not enough nodes specified for start")
	ErrAlreadyBootstrapped    = errors.New("already bootstrapped")
	ErrNotBootstrapped        = errors.New("not bootstrapped")
	ErrNodeNotFound           = errors.New("node not found")
	ErrPeerNotFound           = errors.New("peer not found")
	ErrStatusCanceled         = errors.New("gRPC stream status canceled")
	ErrNoBlockchainSpec       = errors.New("no blockchain spec was provided")
	ErrNoSubnetID             = errors.New("subnetID is missing")
	ErrNoElasticSubnetSpec    = errors.New("no elastic subnet spec was provided")
	ErrNoValidatorSpec        = errors.New("no validator spec was provided")
)

type Config struct {
	Port   string
	GwPort string
	// true to disable grpc-gateway server
	GwDisabled          bool
	DialTimeout         time.Duration
	RedirectNodesOutput bool
	SnapshotsDir        string
	LogLevel            logging.Level
}

type Server interface {
	Run(rootCtx context.Context) error
}

type server struct {
	mu *sync.RWMutex

	cfg Config
	log logging.Logger

	rootCtx    context.Context
	rootCancel context.CancelFunc
	closed     chan struct{}

	ln         net.Listener
	gRPCServer *grpc.Server

	gwMux    *runtime.ServeMux
	gwServer *http.Server

	clusterInfo *rpcpb.ClusterInfo
	// Controls running nodes.
	// Invariant: If [network] is non-nil, then [clusterInfo] is non-nil.

	network    *localNetwork
	asyncErrCh chan error

	rpcpb.UnimplementedPingServiceServer
	rpcpb.UnimplementedControlServiceServer
}

// grpc encapsulates the non protocol-related, ANR server domain errors,
// inside grpc.status.Status structs, with status.Code() code.Unknown,
// and original error msg inside status.Message() string
// this aux function is to be used by clients, to check for the appropriate
// ANR domain error kind
func IsServerError(err error, serverError error) bool {
	status := status.Convert(err)
	return status.Code() == codes.Unknown && status.Message() == serverError.Error()
}

func New(cfg Config, log logging.Logger) (Server, error) {
	if cfg.Port == "" || cfg.GwPort == "" {
		return nil, ErrInvalidPort
	}

	listener, err := net.Listen("tcp", cfg.Port)
	if err != nil {
		return nil, err
	}

	if cfg.SnapshotsDir == "" {
		cfg.SnapshotsDir = local.DefaultSnapshotsDir
	}

	s := &server{
		cfg:        cfg,
		log:        log,
		closed:     make(chan struct{}),
		ln:         listener,
		gRPCServer: grpc.NewServer(),
		mu:         new(sync.RWMutex),
		asyncErrCh: make(chan error, 1),
	}
	if !cfg.GwDisabled {
		s.gwMux = runtime.NewServeMux()
		s.gwServer = &http.Server{ //nolint // TODO add ReadHeaderTimeout
			Addr:    cfg.GwPort,
			Handler: s.gwMux,
		}
	}

	return s, nil
}

// Blocking call until server listeners return.
func (s *server) Run(rootCtx context.Context) (err error) {
	s.rootCtx, s.rootCancel = context.WithCancel(rootCtx)

	rpcpb.RegisterPingServiceServer(s.gRPCServer, s)
	rpcpb.RegisterControlServiceServer(s.gRPCServer, s)

	gRPCErrChan := make(chan error)
	go func() {
		s.log.Info("serving gRPC server", zap.String("port", s.cfg.Port))
		gRPCErrChan <- s.gRPCServer.Serve(s.ln)
	}()

	gwErrChan := make(chan error)
	if s.cfg.GwDisabled {
		s.log.Info("gRPC gateway server is disabled")
	} else {
		// Set up gRPC gateway to allow for HTTP requests to [s.gRPCServer].
		go func() {
			s.log.Info("dialing gRPC server for gRPC gateway", zap.String("port", s.cfg.Port))
			ctx, cancel := context.WithTimeout(rootCtx, s.cfg.DialTimeout)
			gwConn, err := grpc.DialContext(
				ctx,
				"localhost"+s.cfg.Port,
				grpc.WithBlock(),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			cancel()
			if err != nil {
				gwErrChan <- err
				return
			}
			defer gwConn.Close()

			if err := rpcpb.RegisterPingServiceHandler(rootCtx, s.gwMux, gwConn); err != nil {
				gwErrChan <- err
				return
			}
			if err := rpcpb.RegisterControlServiceHandler(rootCtx, s.gwMux, gwConn); err != nil {
				gwErrChan <- err
				return
			}

			s.log.Info("serving gRPC gateway", zap.String("port", s.cfg.GwPort))
			gwErrChan <- s.gwServer.ListenAndServe()
		}()
	}

	select {
	case <-rootCtx.Done():
		s.log.Warn("root context is done")

		if !s.cfg.GwDisabled {
			s.log.Warn("closed gRPC gateway server", zap.Error(s.gwServer.Close()))
			<-gwErrChan
		}

		s.gRPCServer.Stop()
		s.log.Warn("closed gRPC server")
		<-gRPCErrChan // Wait for [s.gRPCServer.Serve] to return.
		s.log.Warn("gRPC terminated")

	case err = <-gRPCErrChan:
		s.log.Warn("gRPC server failed", zap.Error(err))

		// [s.grpcServer] is already stopped.
		if !s.cfg.GwDisabled {
			s.log.Warn("closed gRPC gateway server", zap.Error(s.gwServer.Close()))
			<-gwErrChan
		}

	case err = <-gwErrChan: // if disabled, this will never be selected
		// [s.gwServer] is already closed.
		s.log.Warn("gRPC gateway server failed", zap.Error(err))
		s.gRPCServer.Stop()
		s.log.Warn("closed gRPC server")
		<-gRPCErrChan // Wait for [s.gRPCServer.Serve] to return.
	}

	// Grab lock to ensure [s.network] isn't being used.
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.network != nil {
		// Close the network.
		s.stopAndRemoveNetwork(nil)
		s.log.Warn("network stopped")
	}

	s.rootCancel()
	return err
}

func (s *server) Ping(context.Context, *rpcpb.PingRequest) (*rpcpb.PingResponse, error) {
	s.log.Debug("received ping request")
	return &rpcpb.PingResponse{Pid: int32(os.Getpid())}, nil
}

func (s *server) RPCVersion(context.Context, *rpcpb.RPCVersionRequest) (*rpcpb.RPCVersionResponse, error) {
	s.log.Debug("RPCVersion")

	return &rpcpb.RPCVersionResponse{Version: RPCVersion}, nil
}

func (s *server) Start(_ context.Context, req *rpcpb.StartRequest) (*rpcpb.StartResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If [network] is already populated, the network has already been started.
	if s.network != nil {
		return nil, ErrAlreadyBootstrapped
	}

	// Set default values for [req.NumNodes] if not given.
	if req.NumNodes == nil {
		n := DefaultNodes
		req.NumNodes = &n
	}
	if *req.NumNodes < MinNodes {
		return nil, ErrNotEnoughNodesForStart
	}

	execPath := applyDefaultExecPath(req.GetExecPath())
	pluginDir := applyDefaultPluginDir(req.GetPluginDir())

	if err := utils.CheckExecPath(execPath); err != nil {
		return nil, err
	}

	chainSpecs := []network.BlockchainSpec{}
	if len(req.GetBlockchainSpecs()) > 0 {
		s.log.Info("plugin-dir:", zap.String("plugin-dir", pluginDir))
		for _, spec := range req.GetBlockchainSpecs() {
			chainSpec, err := getNetworkBlockchainSpec(s.log, spec, true, pluginDir)
			if err != nil {
				return nil, err
			}
			chainSpecs = append(chainSpecs, chainSpec)
		}
	}

	var (
		numNodes          = req.GetNumNodes()
		trackSubnets      = req.GetWhitelistedSubnets()
		rootDataDir       = req.GetRootDataDir()
		pid               = int32(os.Getpid())
		globalNodeConfig  = req.GetGlobalNodeConfig()
		customNodeConfigs = req.GetCustomNodeConfigs()
		err               error
	)

	if len(rootDataDir) == 0 {
		rootDataDir = filepath.Join(os.TempDir(), constants.RootDirPrefix)
		err = os.MkdirAll(rootDataDir, os.ModePerm)
		if err != nil {
			return nil, err
		}
		rootDataDir = filepath.Join(rootDataDir, networkRootDirPrefix)
		rootDataDir, err = utils.MkDirWithTimestamp(rootDataDir)
		if err != nil {
			return nil, err
		}
	}

	if len(customNodeConfigs) > 0 {
		s.log.Warn("custom node configs have been provided; ignoring the 'number-of-nodes' parameter and setting it to:", zap.Int("number-of-nodes", len(customNodeConfigs)))
		numNodes = uint32(len(customNodeConfigs))
	}

	s.clusterInfo = &rpcpb.ClusterInfo{
		Pid:         pid,
		RootDataDir: rootDataDir,
	}

	s.network, err = newLocalNetwork(localNetworkOptions{
		networkID:           req.NetworkId,
		execPath:            execPath,
		rootDataDir:         rootDataDir,
		logRootDir:          req.GetLogRootDir(),
		numNodes:            numNodes,
		trackSubnets:        trackSubnets,
		redirectNodesOutput: s.cfg.RedirectNodesOutput,
		pluginDir:           pluginDir,
		globalNodeConfig:    globalNodeConfig,
		customNodeConfigs:   customNodeConfigs,
		chainConfigs:        req.ChainConfigs,
		upgradeConfigs:      req.UpgradeConfigs,
		subnetConfigs:       req.SubnetConfigs,
		logLevel:            s.cfg.LogLevel,
		reassignPortsIfUsed: req.GetReassignPortsIfUsed(),
		dynamicPorts:        req.GetDynamicPorts(),
		snapshotsDir:        s.cfg.SnapshotsDir,
	})
	if err != nil {
		return nil, err
	}

	s.log.Info("starting",
		zap.String("exec-path", execPath),
		zap.Uint32("num-nodes", numNodes),
		zap.String("track-subnets", trackSubnets),
		zap.Int32("pid", pid),
		zap.String("root-data-dir", rootDataDir),
		zap.String("plugin-dir", pluginDir),
		zap.Any("chain-configs", req.ChainConfigs),
		zap.String("global-node-config", globalNodeConfig),
	)

	ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
	defer cancel()
	if err := s.network.Start(ctx); err != nil {
		s.log.Warn("start failed to complete", zap.Error(err))
		s.stopAndRemoveNetwork(nil)
		return nil, err
	}

	ctx, cancel = context.WithTimeout(context.Background(), waitForHealthyTimeout)
	defer cancel()
	chainIDs, err := s.network.CreateChains(ctx, chainSpecs)
	if err != nil {
		s.log.Error("network never became healthy", zap.Error(err))
		s.stopAndRemoveNetwork(err)
		return nil, err
	}
	s.updateClusterInfo()
	s.log.Info("network healthy")

	strChainIDs := []string{}
	for _, chainID := range chainIDs {
		strChainIDs = append(strChainIDs, chainID.String())
	}

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.StartResponse{ClusterInfo: clusterInfo, ChainIds: strChainIDs}, nil
}

// Asssumes [s.mu] is held.
func (s *server) updateClusterInfo() {
	if s.network == nil {
		// stop may have been called
		return
	}
	s.clusterInfo.RootDataDir = s.network.nw.GetRootDir()
	s.clusterInfo.LogRootDir = s.network.nw.GetLogRootDir()
	s.clusterInfo.NetworkId = s.network.networkID
	s.clusterInfo.Healthy = true
	s.clusterInfo.NodeNames = maps.Keys(s.network.nodeInfos)
	sort.Strings(s.clusterInfo.NodeNames)
	s.clusterInfo.NodeInfos = s.network.nodeInfos
	s.clusterInfo.CustomChainsHealthy = true
	s.clusterInfo.CustomChains = make(map[string]*rpcpb.CustomChainInfo)
	for chainID, chainInfo := range s.network.customChainIDToInfo {
		s.clusterInfo.CustomChains[chainID.String()] = chainInfo.info
	}
	s.clusterInfo.Subnets = s.network.subnets
}

// wait until some of this conditions is met:
// - timeout expires
// - network operation terminates with error
// - network operation terminates successfully by setting CustomChainsHealthy
func (s *server) WaitForHealthy(ctx context.Context, _ *rpcpb.WaitForHealthyRequest) (*rpcpb.WaitForHealthyResponse, error) {
	s.log.Debug("WaitForHealthy")

	ctx, cancel := context.WithTimeout(ctx, waitForHealthyTimeout)
	defer cancel()

	for {
		s.mu.RLock()
		if s.clusterInfo == nil {
			defer s.mu.RUnlock()
			return nil, ErrNotBootstrapped
		}
		if s.clusterInfo.CustomChainsHealthy {
			defer s.mu.RUnlock()
			clusterInfo, err := deepCopy(s.clusterInfo)
			if err != nil {
				return nil, err
			}
			return &rpcpb.WaitForHealthyResponse{ClusterInfo: clusterInfo}, nil
		}
		select {
		case err := <-s.asyncErrCh:
			defer s.mu.RUnlock()
			clusterInfo, deepCopyErr := deepCopy(s.clusterInfo)
			if deepCopyErr != nil {
				err = multierr.Append(err, deepCopyErr)
				return nil, err
			}
			return &rpcpb.WaitForHealthyResponse{ClusterInfo: clusterInfo}, err
		case <-ctx.Done():
			defer s.mu.RUnlock()
			clusterInfo, err := deepCopy(s.clusterInfo)
			if err != nil {
				return nil, err
			}
			return &rpcpb.WaitForHealthyResponse{ClusterInfo: clusterInfo}, ctx.Err()
		default:
		}
		if s.network == nil {
			defer s.mu.RUnlock()
			return nil, ErrNotBootstrapped
		}
		s.mu.RUnlock()
		time.Sleep(1 * time.Second)
	}
}

func (s *server) CreateBlockchains(
	ctx context.Context,
	req *rpcpb.CreateBlockchainsRequest,
) (*rpcpb.CreateBlockchainsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	s.log.Debug("CreateBlockchains")

	if len(req.GetBlockchainSpecs()) == 0 {
		return nil, ErrNoBlockchainSpec
	}

	chainSpecs := []network.BlockchainSpec{}
	for _, spec := range req.GetBlockchainSpecs() {
		chainSpec, err := getNetworkBlockchainSpec(s.log, spec, false, s.network.pluginDir)
		if err != nil {
			return nil, err
		}
		chainSpecs = append(chainSpecs, chainSpec)
	}

	// check that the given subnets exist
	subnetsSet := set.Set[string]{}
	subnetIDsList := maps.Keys(s.clusterInfo.Subnets)
	subnetsSet.Add(subnetIDsList...)

	for _, chainSpec := range chainSpecs {
		if chainSpec.SubnetID != nil && !subnetsSet.Contains(*chainSpec.SubnetID) {
			return nil, fmt.Errorf("subnet id %q does not exits", *chainSpec.SubnetID)
		}
	}

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	chainIDs, err := s.network.CreateChains(ctx, chainSpecs)
	if err != nil {
		s.log.Error("failed to create blockchains", zap.Error(err))
		s.stopAndRemoveNetwork(err)
		return nil, err
	} else {
		s.updateClusterInfo()
	}
	s.log.Info("custom chains created")

	strChainIDs := []string{}
	for _, chainID := range chainIDs {
		strChainIDs = append(strChainIDs, chainID.String())
	}

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.CreateBlockchainsResponse{ClusterInfo: clusterInfo, ChainIds: strChainIDs}, nil
}

func (s *server) AddPermissionlessDelegator(
	_ context.Context,
	req *rpcpb.AddPermissionlessDelegatorRequest,
) (*rpcpb.AddPermissionlessDelegatorResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	s.log.Debug("AddPermissionlessDelegator")

	if len(req.GetValidatorSpec()) == 0 {
		return nil, ErrNoValidatorSpec
	}

	delegatorSpecList := []network.PermissionlessStakerSpec{}
	for _, spec := range req.GetValidatorSpec() {
		validatorSpec, err := getPermissionlessValidatorSpec(spec)
		if err != nil {
			return nil, err
		}
		delegatorSpecList = append(delegatorSpecList, validatorSpec)
	}

	// check that the given subnets exist
	subnetsSet := set.Set[string]{}
	subnetsSet.Add(maps.Keys(s.clusterInfo.Subnets)...)

	for _, validatorSpec := range delegatorSpecList {
		if validatorSpec.SubnetID == "" {
			return nil, ErrNoSubnetID
		} else if !subnetsSet.Contains(validatorSpec.SubnetID) {
			return nil, fmt.Errorf("subnet id %q does not exist", validatorSpec.SubnetID)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
	defer cancel()
	err := s.network.AddPermissionlessDelegators(ctx, delegatorSpecList)
	if err != nil {
		s.log.Error("failed to add permissionless delegator", zap.Error(err))
		return nil, err
	}

	s.log.Info("successfully added permissionless delegator")

	if err != nil {
		return nil, err
	}
	return &rpcpb.AddPermissionlessDelegatorResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) AddPermissionlessValidator(
	_ context.Context,
	req *rpcpb.AddPermissionlessValidatorRequest,
) (*rpcpb.AddPermissionlessValidatorResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	s.log.Debug("AddPermissionlessValidator")

	if len(req.GetValidatorSpec()) == 0 {
		return nil, ErrNoValidatorSpec
	}

	validatorSpecList := []network.PermissionlessStakerSpec{}
	for _, spec := range req.GetValidatorSpec() {
		validatorSpec, err := getPermissionlessValidatorSpec(spec)
		if err != nil {
			return nil, err
		}
		validatorSpecList = append(validatorSpecList, validatorSpec)
	}

	// check that the given subnets exist
	subnetsSet := set.Set[string]{}
	subnetsSet.Add(maps.Keys(s.clusterInfo.Subnets)...)

	for _, validatorSpec := range validatorSpecList {
		if validatorSpec.SubnetID == "" {
			return nil, ErrNoSubnetID
		} else if !subnetsSet.Contains(validatorSpec.SubnetID) {
			return nil, fmt.Errorf("subnet id %q does not exist", validatorSpec.SubnetID)
		}
	}

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
	defer cancel()
	err := s.network.AddPermissionlessValidators(ctx, validatorSpecList)

	s.updateClusterInfo()

	if err != nil {
		s.log.Error("failed to add permissionless validator", zap.Error(err))
		return nil, err
	}

	s.log.Info("successfully added permissionless validator")

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.AddPermissionlessValidatorResponse{ClusterInfo: clusterInfo}, nil
}

func (s *server) AddSubnetValidators(
	_ context.Context,
	req *rpcpb.AddSubnetValidatorsRequest,
) (*rpcpb.AddSubnetValidatorsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	s.log.Debug("AddSubnetValidators")

	if len(req.GetValidatorsSpec()) == 0 {
		return nil, ErrNoValidatorSpec
	}

	validatorSpecList := []network.SubnetValidatorsSpec{}
	for _, spec := range req.GetValidatorsSpec() {
		validatorSpec := getSubnetValidatorSpec(spec)
		validatorSpecList = append(validatorSpecList, validatorSpec)
	}

	// check that the given subnets exist
	subnetsSet := set.Set[string]{}
	subnetsSet.Add(maps.Keys(s.clusterInfo.Subnets)...)

	for _, validatorSpec := range validatorSpecList {
		if validatorSpec.SubnetID == "" {
			return nil, ErrNoSubnetID
		} else if !subnetsSet.Contains(validatorSpec.SubnetID) {
			return nil, fmt.Errorf("subnet id %q does not exist", validatorSpec.SubnetID)
		}
	}

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
	defer cancel()
	err := s.network.AddSubnetValidators(ctx, validatorSpecList)

	s.updateClusterInfo()

	if err != nil {
		s.log.Error("failed to add subnet validators", zap.Error(err))
		return nil, err
	}

	s.log.Info("successfully added subnet validators")

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.AddSubnetValidatorsResponse{ClusterInfo: clusterInfo}, nil
}

func (s *server) RemoveSubnetValidator(
	_ context.Context,
	req *rpcpb.RemoveSubnetValidatorRequest,
) (*rpcpb.RemoveSubnetValidatorResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	s.log.Debug("RemoveSubnetValidator")

	if len(req.GetValidatorSpec()) == 0 {
		return nil, ErrNoValidatorSpec
	}

	validatorSpecList := []network.SubnetValidatorsSpec{}
	for _, spec := range req.GetValidatorSpec() {
		validatorSpec := getRemoveSubnetValidatorSpec(spec)
		validatorSpecList = append(validatorSpecList, validatorSpec)
	}

	// check that the given subnets exist
	subnetsSet := set.Set[string]{}
	subnetsSet.Add(maps.Keys(s.clusterInfo.Subnets)...)

	for _, validatorSpec := range validatorSpecList {
		if validatorSpec.SubnetID == "" {
			return nil, ErrNoSubnetID
		} else if !subnetsSet.Contains(validatorSpec.SubnetID) {
			return nil, fmt.Errorf("subnet id %q does not exist", validatorSpec.SubnetID)
		}
	}

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
	defer cancel()
	err := s.network.RemoveSubnetValidator(ctx, validatorSpecList)

	s.updateClusterInfo()

	if err != nil {
		s.log.Error("failed to remove subnet validator", zap.Error(err))
		return nil, err
	}

	s.log.Info("successfully removed subnet validator")

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.RemoveSubnetValidatorResponse{ClusterInfo: clusterInfo}, nil
}

func (s *server) TransformElasticSubnets(
	_ context.Context,
	req *rpcpb.TransformElasticSubnetsRequest,
) (*rpcpb.TransformElasticSubnetsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	s.log.Debug("TransformElasticSubnet")

	if len(req.GetElasticSubnetSpec()) == 0 {
		return nil, ErrNoElasticSubnetSpec
	}

	elasticSubnetSpecList := []network.ElasticSubnetSpec{}
	for _, spec := range req.GetElasticSubnetSpec() {
		elasticSubnetSpec := getNetworkElasticSubnetSpec(spec)
		elasticSubnetSpecList = append(elasticSubnetSpecList, elasticSubnetSpec)
	}

	// check that the given subnets exist
	subnetsSet := set.Set[string]{}
	subnetsSet.Add(maps.Keys(s.clusterInfo.Subnets)...)

	for _, elasticSubnetSpec := range elasticSubnetSpecList {
		if elasticSubnetSpec.SubnetID == nil {
			return nil, ErrNoSubnetID
		} else if !subnetsSet.Contains(*elasticSubnetSpec.SubnetID) {
			return nil, fmt.Errorf("subnet id %q does not exist", *elasticSubnetSpec.SubnetID)
		}
	}

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
	defer cancel()
	txIDs, assetIDs, err := s.network.TransformSubnets(ctx, elasticSubnetSpecList)

	s.updateClusterInfo()

	if err != nil {
		s.log.Error("failed to transform subnet into elastic subnet", zap.Error(err))
		return nil, err
	}

	s.log.Info("subnet transformed into elastic subnet")

	strTXIDs := []string{}
	for _, txID := range txIDs {
		strTXIDs = append(strTXIDs, txID.String())
	}

	strAssetIDs := []string{}
	for _, assetID := range assetIDs {
		strAssetIDs = append(strAssetIDs, assetID.String())
	}

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.TransformElasticSubnetsResponse{ClusterInfo: clusterInfo, TxIds: strTXIDs, AssetIds: strAssetIDs}, nil
}

func (s *server) CreateSubnets(_ context.Context, req *rpcpb.CreateSubnetsRequest) (*rpcpb.CreateSubnetsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	s.log.Debug("CreateSubnets", zap.Uint32("num-subnets", uint32(len(req.GetSubnetSpecs()))))

	subnetSpecs := []network.SubnetSpec{}
	for _, spec := range req.GetSubnetSpecs() {
		subnetSpec := getNetworkSubnetSpec(spec)
		subnetSpecs = append(subnetSpecs, subnetSpec)
	}

	s.log.Info("waiting for local cluster readiness")

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
	defer cancel()
	subnetIDs, err := s.network.CreateSubnets(ctx, subnetSpecs)
	if err != nil {
		s.log.Error("failed to create subnets", zap.Error(err))
		s.stopAndRemoveNetwork(err)
		return nil, err
	} else {
		s.updateClusterInfo()
	}
	s.log.Info("subnets created")

	strSubnetIDs := []string{}
	for _, subnetID := range subnetIDs {
		strSubnetIDs = append(strSubnetIDs, subnetID.String())
	}

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.CreateSubnetsResponse{ClusterInfo: clusterInfo, SubnetIds: strSubnetIDs}, nil
}

func (s *server) Health(ctx context.Context, _ *rpcpb.HealthRequest) (*rpcpb.HealthResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("Health")

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	s.log.Info("waiting for local cluster readiness")
	if err := s.network.AwaitHealthyAndUpdateNetworkInfo(ctx); err != nil {
		return nil, err
	}

	s.clusterInfo.NodeNames = maps.Keys(s.network.nodeInfos)
	sort.Strings(s.clusterInfo.NodeNames)
	s.clusterInfo.NodeInfos = s.network.nodeInfos
	s.clusterInfo.Healthy = true

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.HealthResponse{ClusterInfo: clusterInfo}, nil
}

func (s *server) URIs(context.Context, *rpcpb.URIsRequest) (*rpcpb.URIsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.log.Debug("URIs")

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	uris := make([]string, 0, len(s.clusterInfo.NodeInfos))
	for _, nodeInfo := range s.clusterInfo.NodeInfos {
		uris = append(uris, nodeInfo.Uri)
	}
	sort.Strings(uris)

	return &rpcpb.URIsResponse{Uris: uris}, nil
}

func (s *server) Status(context.Context, *rpcpb.StatusRequest) (*rpcpb.StatusResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.log.Debug("Status")

	if s.network == nil {
		return &rpcpb.StatusResponse{}, ErrNotBootstrapped
	}

	return &rpcpb.StatusResponse{ClusterInfo: s.clusterInfo}, nil
}

// Assumes [s.mu] is held.
func (s *server) stopAndRemoveNetwork(err error) {
	s.log.Info("removing network")
	select {
	// cleanup of possible previous unchecked async err
	case err := <-s.asyncErrCh:
		s.log.Debug(fmt.Sprintf("async err %s not returned to user", err))
	default:
	}
	if err != nil {
		s.asyncErrCh <- err
	}
	if s.network != nil {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		s.network.Stop(ctx)
	}
	if s.clusterInfo != nil {
		s.clusterInfo.Healthy = false
		s.clusterInfo.CustomChainsHealthy = false
	}
	s.network = nil
}

// TODO document this
func (s *server) StreamStatus(req *rpcpb.StreamStatusRequest, stream rpcpb.ControlService_StreamStatusServer) (err error) {
	s.log.Debug("StreamStatus")

	interval := time.Duration(req.PushInterval)

	// returns this method, then server closes the stream
	s.log.Info("pushing status updates to the stream", zap.String("interval", interval.String()))
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		s.sendLoop(stream, interval)
		wg.Done()
	}()

	errCh := make(chan error, 1)
	go func() {
		err := s.recvLoop(stream)
		if err != nil {
			if isClientCanceled(stream.Context().Err(), err) {
				s.log.Warn("failed to receive status request from gRPC stream due to client cancellation", zap.Error(err))
			} else {
				s.log.Warn("failed to receive status request from gRPC stream", zap.Error(err))
			}
		}
		errCh <- err
	}()

	select {
	case err = <-errCh:
		if errors.Is(err, context.Canceled) {
			err = ErrStatusCanceled
		}
	case <-stream.Context().Done():
		err = stream.Context().Err()
		if errors.Is(err, context.Canceled) {
			err = ErrStatusCanceled
		}
	}

	wg.Wait()
	return err
}

// TODO document this
func (s *server) sendLoop(stream rpcpb.ControlService_StreamStatusServer, interval time.Duration) {
	s.log.Info("start status send loop")

	tc := time.NewTicker(1)
	defer tc.Stop()

	for {
		select {
		case <-s.rootCtx.Done():
			return
		case <-tc.C:
			tc.Reset(interval)
		}

		s.log.Debug("sending cluster info")

		s.mu.RLock()
		err := stream.Send(&rpcpb.StreamStatusResponse{ClusterInfo: s.clusterInfo})
		s.mu.RUnlock()
		if err != nil {
			if isClientCanceled(stream.Context().Err(), err) {
				s.log.Debug("client stream canceled", zap.Error(err))
				return
			}
			s.log.Warn("failed to send an event", zap.Error(err))
			return
		}
	}
}

// TODO document this
func (s *server) recvLoop(stream rpcpb.ControlService_StreamStatusServer) error {
	s.log.Info("start status receive loop")

	for {
		select {
		case <-s.rootCtx.Done():
			return s.rootCtx.Err()
		default:
		}

		// receive data from stream
		req := new(rpcpb.StatusRequest)
		err := stream.RecvMsg(req)
		if errors.Is(err, io.EOF) {
			s.log.Debug("received EOF from client; returning to close the stream from server side")
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *server) AddNode(_ context.Context, req *rpcpb.AddNodeRequest) (*rpcpb.AddNodeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("AddNode", zap.String("name", req.Name))

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	nodeFlags := map[string]interface{}{}
	if req.GetNodeConfig() != "" {
		if err := json.Unmarshal([]byte(req.GetNodeConfig()), &nodeFlags); err != nil {
			return nil, err
		}
	}

	pluginDir := applyDefaultPluginDir(req.GetPluginDir())
	if pluginDir != "" {
		nodeFlags[config.PluginDirKey] = pluginDir
	}

	nodeConfig := node.Config{
		Name:               req.Name,
		Flags:              nodeFlags,
		BinaryPath:         applyDefaultExecPath(req.GetExecPath()),
		RedirectStdout:     s.cfg.RedirectNodesOutput,
		RedirectStderr:     s.cfg.RedirectNodesOutput,
		ChainConfigFiles:   req.ChainConfigs,
		UpgradeConfigFiles: req.UpgradeConfigs,
		SubnetConfigFiles:  req.SubnetConfigs,
	}

	if _, err := s.network.nw.AddNode(nodeConfig); err != nil {
		return nil, err
	}

	if err := s.network.UpdateNodeInfo(); err != nil {
		return nil, err
	}

	s.clusterInfo.NodeNames = maps.Keys(s.network.nodeInfos)
	sort.Strings(s.clusterInfo.NodeNames)
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.AddNodeResponse{ClusterInfo: clusterInfo}, nil
}

func (s *server) RemoveNode(ctx context.Context, req *rpcpb.RemoveNodeRequest) (*rpcpb.RemoveNodeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("RemoveNode", zap.String("name", req.Name))

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	if err := s.network.nw.RemoveNode(ctx, req.Name); err != nil {
		return nil, err
	}

	if err := s.network.UpdateNodeInfo(); err != nil {
		return nil, err
	}

	s.clusterInfo.NodeNames = maps.Keys(s.network.nodeInfos)
	sort.Strings(s.clusterInfo.NodeNames)
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.RemoveNodeResponse{ClusterInfo: clusterInfo}, nil
}

func (s *server) RestartNode(ctx context.Context, req *rpcpb.RestartNodeRequest) (*rpcpb.RestartNodeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("RestartNode", zap.String("name", req.Name))

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	if err := s.network.nw.RestartNode(
		ctx,
		req.Name,
		applyDefaultExecPath(req.GetExecPath()),
		applyDefaultPluginDir(req.GetPluginDir()),
		req.GetWhitelistedSubnets(),
		req.GetChainConfigs(),
		req.GetUpgradeConfigs(),
		req.GetSubnetConfigs(),
	); err != nil {
		return nil, err
	}

	if err := s.network.UpdateNodeInfo(); err != nil {
		return nil, err
	}

	s.clusterInfo.NodeNames = maps.Keys(s.network.nodeInfos)
	sort.Strings(s.clusterInfo.NodeNames)
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.RestartNodeResponse{ClusterInfo: clusterInfo}, nil
}

func (s *server) PauseNode(ctx context.Context, req *rpcpb.PauseNodeRequest) (*rpcpb.PauseNodeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("PauseNode", zap.String("name", req.Name))

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	if err := s.network.nw.PauseNode(
		ctx,
		req.Name,
	); err != nil {
		return nil, err
	}

	if err := s.network.UpdateNodeInfo(); err != nil {
		return nil, err
	}

	s.clusterInfo.NodeNames = maps.Keys(s.network.nodeInfos)
	sort.Strings(s.clusterInfo.NodeNames)
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	return &rpcpb.PauseNodeResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) ResumeNode(ctx context.Context, req *rpcpb.ResumeNodeRequest) (*rpcpb.ResumeNodeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("ResumeNode", zap.String("name", req.Name))

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	if err := s.network.nw.ResumeNode(
		ctx,
		req.Name,
	); err != nil {
		return nil, err
	}

	if err := s.network.UpdateNodeInfo(); err != nil {
		return nil, err
	}

	s.clusterInfo.NodeNames = maps.Keys(s.network.nodeInfos)
	sort.Strings(s.clusterInfo.NodeNames)
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	return &rpcpb.ResumeNodeResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) Stop(context.Context, *rpcpb.StopRequest) (*rpcpb.StopResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("Stop")

	s.stopAndRemoveNetwork(nil)

	return &rpcpb.StopResponse{ClusterInfo: s.clusterInfo}, nil
}

var _ router.InboundHandler = &loggingInboundHandler{}

type loggingInboundHandler struct {
	nodeName string
	log      logging.Logger
}

func (lh *loggingInboundHandler) HandleInbound(_ context.Context, m message.InboundMessage) {
	lh.log.Debug(
		"inbound handler received a message",
		zap.String("message", m.Op().String()),
		zap.String("node-name", lh.nodeName),
	)
}

func (s *server) AttachPeer(ctx context.Context, req *rpcpb.AttachPeerRequest) (*rpcpb.AttachPeerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("AttachPeer")

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	node, err := s.network.nw.GetNode(req.NodeName)
	if err != nil {
		return nil, err
	}

	loggingHandler := &loggingInboundHandler{nodeName: req.NodeName, log: s.log}
	newPeer, err := node.AttachPeer(ctx, loggingHandler)
	if err != nil {
		return nil, err
	}

	newPeerID := newPeer.ID().String()
	s.log.Debug("new peer is attached to", zap.String("peer-ID", newPeerID), zap.String("node-name", node.GetName()))

	if s.clusterInfo.AttachedPeerInfos == nil {
		s.clusterInfo.AttachedPeerInfos = make(map[string]*rpcpb.ListOfAttachedPeerInfo)
	}
	peerInfo := &rpcpb.AttachedPeerInfo{Id: newPeerID}
	if v, ok := s.clusterInfo.AttachedPeerInfos[req.NodeName]; ok {
		v.Peers = append(v.Peers, peerInfo)
	} else {
		s.clusterInfo.AttachedPeerInfos[req.NodeName] = &rpcpb.ListOfAttachedPeerInfo{
			Peers: []*rpcpb.AttachedPeerInfo{peerInfo},
		}
	}

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.AttachPeerResponse{ClusterInfo: clusterInfo, AttachedPeerInfo: peerInfo}, nil
}

func (s *server) SendOutboundMessage(ctx context.Context, req *rpcpb.SendOutboundMessageRequest) (*rpcpb.SendOutboundMessageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("SendOutboundMessage")

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	node, err := s.network.nw.GetNode(req.NodeName)
	if err != nil {
		return nil, err
	}

	sent, err := node.SendOutboundMessage(ctx, req.PeerId, req.Bytes, req.Op)
	return &rpcpb.SendOutboundMessageResponse{Sent: sent}, err
}

func (s *server) LoadSnapshot(_ context.Context, req *rpcpb.LoadSnapshotRequest) (*rpcpb.LoadSnapshotResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Debug("LoadSnapshot")

	if s.network != nil {
		return nil, ErrAlreadyBootstrapped
	}

	var err error
	rootDataDir := req.GetRootDataDir()
	if len(rootDataDir) == 0 && !req.InPlace {
		rootDataDir = filepath.Join(os.TempDir(), constants.RootDirPrefix)
		err = os.MkdirAll(rootDataDir, os.ModePerm)
		if err != nil {
			return nil, err
		}
		rootDataDir = filepath.Join(rootDataDir, networkRootDirPrefix)
		rootDataDir, err = utils.MkDirWithTimestamp(rootDataDir)
		if err != nil {
			return nil, err
		}
	}

	pid := int32(os.Getpid())
	s.log.Info("starting", zap.Int32("pid", pid), zap.String("root-data-dir", req.GetRootDataDir()))

	s.network, err = newLocalNetwork(localNetworkOptions{
		execPath:            applyDefaultExecPath(req.GetExecPath()),
		pluginDir:           applyDefaultPluginDir(req.GetPluginDir()),
		rootDataDir:         rootDataDir,
		logRootDir:          req.GetLogRootDir(),
		chainConfigs:        req.ChainConfigs,
		upgradeConfigs:      req.UpgradeConfigs,
		subnetConfigs:       req.SubnetConfigs,
		globalNodeConfig:    req.GetGlobalNodeConfig(),
		logLevel:            s.cfg.LogLevel,
		reassignPortsIfUsed: req.GetReassignPortsIfUsed(),
		snapshotsDir:        s.cfg.SnapshotsDir,
	})
	if err != nil {
		return nil, err
	}
	s.clusterInfo = &rpcpb.ClusterInfo{
		Pid: pid,
	}

	// blocking load snapshot to soon get not found snapshot errors
	if err := s.network.LoadSnapshot(req.SnapshotName, req.InPlace); err != nil {
		s.log.Warn("snapshot load failed to complete", zap.Error(err))
		s.stopAndRemoveNetwork(nil)
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
	defer cancel()
	err = s.network.AwaitHealthyAndUpdateNetworkInfo(ctx)
	if err != nil {
		s.log.Warn("snapshot load failed to complete. stopping network and cleaning up network", zap.Error(err))
		s.stopAndRemoveNetwork(err)
		return nil, err
	}
	s.updateClusterInfo()
	s.log.Info("network healthy")

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.LoadSnapshotResponse{ClusterInfo: clusterInfo}, nil
}

func (s *server) SaveSnapshot(ctx context.Context, req *rpcpb.SaveSnapshotRequest) (*rpcpb.SaveSnapshotResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Info("SaveSnapshot", zap.String("snapshot-name", req.SnapshotName))

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	snapshotPath, err := s.network.nw.SaveSnapshot(ctx, req.SnapshotName, req.Force)
	if err != nil {
		s.log.Warn("snapshot save failed to complete", zap.Error(err))
		return nil, err
	}

	s.stopAndRemoveNetwork(nil)

	return &rpcpb.SaveSnapshotResponse{SnapshotPath: snapshotPath}, nil
}

func (s *server) RemoveSnapshot(_ context.Context, req *rpcpb.RemoveSnapshotRequest) (*rpcpb.RemoveSnapshotResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.log.Info("RemoveSnapshot", zap.String("snapshot-name", req.SnapshotName))

	if err := local.RemoveSnapshot(s.cfg.SnapshotsDir, req.SnapshotName); err != nil {
		s.log.Warn("snapshot remove failed to complete", zap.Error(err))
		return nil, err
	}
	return &rpcpb.RemoveSnapshotResponse{}, nil
}

func (s *server) GetSnapshotNames(context.Context, *rpcpb.GetSnapshotNamesRequest) (*rpcpb.GetSnapshotNamesResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.log.Info("GetSnapshotNames")

	snapshotNames, err := local.GetSnapshotNames(s.cfg.SnapshotsDir)
	if err != nil {
		return nil, err
	}
	return &rpcpb.GetSnapshotNamesResponse{SnapshotNames: snapshotNames}, nil
}

func (s *server) ListSubnets(context.Context, *rpcpb.ListSubnetsRequest) (*rpcpb.ListSubnetsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.log.Info("ListSubnets")

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	subnetIDs := maps.Keys(s.clusterInfo.Subnets)

	return &rpcpb.ListSubnetsResponse{SubnetIds: subnetIDs}, nil
}

func (s *server) ListBlockchains(context.Context, *rpcpb.ListBlockchainsRequest) (*rpcpb.ListBlockchainsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.log.Info("ListBlockchains")

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	blockchains := maps.Values(s.clusterInfo.CustomChains)

	return &rpcpb.ListBlockchainsResponse{Blockchains: blockchains}, nil
}

func (s *server) ListRpcs(context.Context, *rpcpb.ListRpcsRequest) (*rpcpb.ListRpcsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.log.Info("ListRpcs")

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	blockchainsRpcs := []*rpcpb.BlockchainRpcs{}
	for _, chain := range s.clusterInfo.CustomChains {
		subnetInfo, ok := s.clusterInfo.Subnets[chain.SubnetId]
		if !ok {
			return nil, fmt.Errorf("subnet %q not found in subnet info", chain.SubnetId)
		}
		nodeNames := subnetInfo.SubnetParticipants.NodeNames
		sort.Strings(nodeNames)
		rpcs := []*rpcpb.NodeRpc{}
		for _, nodeName := range nodeNames {
			nodeInfo, ok := s.clusterInfo.NodeInfos[nodeName]
			if !ok {
				return nil, fmt.Errorf("node %q not found in node info", nodeName)
			}
			rpc := fmt.Sprintf("%s/ext/bc/%s/rpc", nodeInfo.Uri, chain.ChainId)
			nodeRPC := rpcpb.NodeRpc{
				NodeName: nodeName,
				Rpc:      rpc,
			}
			rpcs = append(rpcs, &nodeRPC)
		}
		blockchainRpcs := rpcpb.BlockchainRpcs{
			BlockchainId: chain.ChainId,
			Rpcs:         rpcs,
		}
		blockchainsRpcs = append(blockchainsRpcs, &blockchainRpcs)
	}

	return &rpcpb.ListRpcsResponse{BlockchainsRpcs: blockchainsRpcs}, nil
}

func (s *server) VMID(_ context.Context, req *rpcpb.VMIDRequest) (*rpcpb.VMIDResponse, error) {
	s.log.Info("VMID")

	vmID, err := utils.VMID(req.VmName)
	if err != nil {
		return nil, err
	}

	return &rpcpb.VMIDResponse{VmId: vmID.String()}, nil
}

func isClientCanceled(ctxErr error, err error) bool {
	if ctxErr != nil {
		return true
	}

	ev, ok := status.FromError(err)
	if !ok {
		return false
	}

	switch ev.Code() {
	case codes.Canceled, codes.DeadlineExceeded:
		// client-side context cancel or deadline exceeded
		// "rpc error: code = Canceled desc = context canceled"
		// "rpc error: code = DeadlineExceeded desc = context deadline exceeded"
		return true
	case codes.Unavailable:
		msg := ev.Message()
		// client-side context cancel or deadline exceeded with TLS ("http2.errClientDisconnected")
		// "rpc error: code = Unavailable desc = client disconnected"
		if msg == "client disconnected" {
			return true
		}
		// "grpc/transport.ClientTransport.CloseStream" on canceled streams
		// "rpc error: code = Unavailable desc = stream error: stream ID 21; CANCEL")
		if strings.HasPrefix(msg, "stream error: ") && strings.HasSuffix(msg, "; CANCEL") {
			return true
		}
	}
	return false
}

func getNetworkElasticSubnetSpec(
	spec *rpcpb.ElasticSubnetSpec,
) network.ElasticSubnetSpec {
	minStakeDuration := time.Duration(spec.MinStakeDuration) * time.Hour
	maxStakeDuration := time.Duration(spec.MaxStakeDuration) * time.Hour

	elasticSubnetSpec := network.ElasticSubnetSpec{
		SubnetID:                 &spec.SubnetId,
		AssetName:                spec.AssetName,
		AssetSymbol:              spec.AssetSymbol,
		InitialSupply:            spec.InitialSupply,
		MaxSupply:                spec.MaxSupply,
		MinConsumptionRate:       spec.MinConsumptionRate,
		MaxConsumptionRate:       spec.MaxConsumptionRate,
		MinValidatorStake:        spec.MinValidatorStake,
		MaxValidatorStake:        spec.MaxValidatorStake,
		MinStakeDuration:         minStakeDuration,
		MaxStakeDuration:         maxStakeDuration,
		MinDelegationFee:         spec.MinDelegationFee,
		MinDelegatorStake:        spec.MinDelegatorStake,
		MaxValidatorWeightFactor: byte(spec.MaxValidatorWeightFactor),
		UptimeRequirement:        spec.UptimeRequirement,
	}
	return elasticSubnetSpec
}

func getPermissionlessValidatorSpec(
	spec *rpcpb.PermissionlessStakerSpec,
) (network.PermissionlessStakerSpec, error) {
	var startTime time.Time
	var err error
	if spec.StartTime != "" {
		startTime, err = time.Parse(TimeParseLayout, spec.StartTime)
		if err != nil {
			return network.PermissionlessStakerSpec{}, err
		}
		if startTime.Before(time.Now().Add(StakingMinimumLeadTime)) {
			return network.PermissionlessStakerSpec{}, fmt.Errorf("time should be at least %s in the future for validator spec of %s", StakingMinimumLeadTime, spec.NodeName)
		}
	}

	stakeDuration := time.Duration(spec.StakeDuration) * time.Hour

	validatorSpec := network.PermissionlessStakerSpec{
		SubnetID:      spec.SubnetId,
		AssetID:       spec.AssetId,
		NodeName:      spec.NodeName,
		StakedAmount:  spec.StakedTokenAmount,
		StartTime:     startTime,
		StakeDuration: stakeDuration,
	}
	return validatorSpec, nil
}

func getSubnetValidatorSpec(
	spec *rpcpb.SubnetValidatorsSpec,
) network.SubnetValidatorsSpec {
	return network.SubnetValidatorsSpec{
		SubnetID:  spec.SubnetId,
		NodeNames: spec.GetNodeNames(),
	}
}

func getRemoveSubnetValidatorSpec(
	spec *rpcpb.RemoveSubnetValidatorSpec,
) network.SubnetValidatorsSpec {
	return network.SubnetValidatorsSpec{
		SubnetID:  spec.SubnetId,
		NodeNames: spec.GetNodeNames(),
	}
}

func getNetworkBlockchainSpec(
	log logging.Logger,
	spec *rpcpb.BlockchainSpec,
	isNewEmptyNetwork bool,
	pluginDir string,
) (network.BlockchainSpec, error) {
	if isNewEmptyNetwork && spec.SubnetId != nil {
		return network.BlockchainSpec{}, errors.New("blockchain subnet id must be nil if starting a new empty network")
	}

	vmName := spec.VmName
	log.Info("checking custom chain's VM ID before installation", zap.String("id", vmName))
	vmID, err := utils.VMID(vmName)
	if err != nil {
		log.Warn("failed to convert VM name to VM ID", zap.String("vm-name", vmName), zap.Error(err))
		return network.BlockchainSpec{}, ErrInvalidVMName
	}

	// there is no default plugindir from the ANR point of view, will not check if not given
	if pluginDir != "" {
		if err := utils.CheckPluginPath(
			filepath.Join(pluginDir, vmID.String()),
		); err != nil {
			return network.BlockchainSpec{}, err
		}
	}

	genesisBytes := readFileOrString(spec.Genesis)

	var chainConfigBytes []byte
	if spec.ChainConfig != "" {
		chainConfigBytes = readFileOrString(spec.ChainConfig)
	}

	var networkUpgradeBytes []byte
	if spec.NetworkUpgrade != "" {
		networkUpgradeBytes = readFileOrString(spec.NetworkUpgrade)
	}

	var subnetConfigBytes []byte
	if spec.SubnetSpec != nil && spec.SubnetSpec.SubnetConfig != "" {
		subnetConfigBytes = readFileOrString(spec.SubnetSpec.SubnetConfig)
	}

	perNodeChainConfig := map[string][]byte{}
	if spec.PerNodeChainConfig != "" {
		perNodeChainConfigBytes := readFileOrString(spec.PerNodeChainConfig)

		perNodeChainConfigMap := map[string]interface{}{}
		if err := json.Unmarshal(perNodeChainConfigBytes, &perNodeChainConfigMap); err != nil {
			return network.BlockchainSpec{}, err
		}

		for nodeName, cfg := range perNodeChainConfigMap {
			cfgBytes, err := json.Marshal(cfg)
			if err != nil {
				return network.BlockchainSpec{}, err
			}
			perNodeChainConfig[nodeName] = cfgBytes
		}
	}

	blockchainSpec := network.BlockchainSpec{
		VMName:             vmName,
		Genesis:            genesisBytes,
		ChainConfig:        chainConfigBytes,
		NetworkUpgrade:     networkUpgradeBytes,
		SubnetID:           spec.SubnetId,
		BlockchainAlias:    spec.BlockchainAlias,
		PerNodeChainConfig: perNodeChainConfig,
	}

	if spec.SubnetSpec != nil {
		subnetSpec := network.SubnetSpec{
			Participants: spec.SubnetSpec.Participants,
			SubnetConfig: subnetConfigBytes,
		}
		blockchainSpec.SubnetSpec = &subnetSpec
	}

	return blockchainSpec, nil
}

func getNetworkSubnetSpec(
	spec *rpcpb.SubnetSpec,
) network.SubnetSpec {
	var subnetConfigBytes []byte
	if spec.SubnetConfig != "" {
		subnetConfigBytes = readFileOrString(spec.SubnetConfig)
	}
	return network.SubnetSpec{
		Participants: spec.Participants,
		SubnetConfig: subnetConfigBytes,
	}
}

// if [conf] is a readable file path, returns the file contents
// if not, returns [conf] as a byte slice
func readFileOrString(conf string) []byte {
	confBytes, err := os.ReadFile(conf)
	if err != nil {
		return []byte(conf)
	}
	return confBytes
}

// if [userGivenExecPath] is non empty, returns it
// otherwise if env var utils.DefaultExecPathEnvVar is
// defined, returns its contents
func applyDefaultExecPath(userGivenExecPath string) string {
	if userGivenExecPath != "" {
		return userGivenExecPath
	}
	return os.Getenv(constants.DefaultExecPathEnvVar)
}

// if [userGivenPluginDir] is non empty, returns it
// otherwise if env var utils.DefaultPluginDirEnvVar is
// defined, returns its contents
func applyDefaultPluginDir(userGivenPluginDir string) string {
	if userGivenPluginDir != "" {
		return userGivenPluginDir
	}
	return os.Getenv(constants.DefaultPluginDirEnvVar)
}
