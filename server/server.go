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

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/utils"
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

	stopTimeout           = 5 * time.Second
	defaultStartTimeout   = 5 * time.Minute
	waitForHealthyTimeout = 2 * time.Minute

	rootDataDirPrefix = "network-runner-root-data"
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
				"0.0.0.0"+s.cfg.Port,
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

	if err := utils.CheckExecPath(req.GetExecPath()); err != nil {
		return nil, err
	}

	pluginDir := req.GetPluginDir()
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
		execPath          = req.GetExecPath()
		numNodes          = req.GetNumNodes()
		trackSubnets      = req.GetWhitelistedSubnets()
		rootDataDir       = req.GetRootDataDir()
		pid               = int32(os.Getpid())
		globalNodeConfig  = req.GetGlobalNodeConfig()
		customNodeConfigs = req.GetCustomNodeConfigs()
		err               error
	)

	if len(rootDataDir) == 0 {
		rootDataDir = os.TempDir()
	}
	rootDataDir = filepath.Join(rootDataDir, rootDataDirPrefix)
	rootDataDir, err = utils.MkDirWithTimestamp(rootDataDir)
	if err != nil {
		return nil, err
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
		execPath:            execPath,
		rootDataDir:         rootDataDir,
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

	if err := s.network.Start(); err != nil {
		s.log.Warn("start failed to complete", zap.Error(err))
		s.stopAndRemoveNetwork(nil)
		return nil, err
	}

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
		defer cancel()
		err := s.network.CreateChains(ctx, chainSpecs)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.log.Error("network never became healthy", zap.Error(err))
			s.stopAndRemoveNetwork(err)
			return
		}
		s.updateClusterInfo()
		s.log.Info("network healthy")
	}()

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.StartResponse{ClusterInfo: clusterInfo}, nil
}

// Asssumes [s.mu] is held.
func (s *server) updateClusterInfo() {
	if s.network == nil {
		// stop may have been called
		return
	}
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
	_ context.Context,
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
	subnetsSet.Add(s.clusterInfo.Subnets...)

	for _, chainSpec := range chainSpecs {
		if chainSpec.SubnetID != nil && !subnetsSet.Contains(*chainSpec.SubnetID) {
			return nil, fmt.Errorf("subnet id %q does not exits", *chainSpec.SubnetID)
		}
	}

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
		defer cancel()
		err := s.network.CreateChains(ctx, chainSpecs)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.log.Error("failed to create blockchains", zap.Error(err))
			s.stopAndRemoveNetwork(err)
			return
		} else {
			s.updateClusterInfo()
		}
		s.log.Info("custom chains created")
	}()
	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.CreateBlockchainsResponse{ClusterInfo: clusterInfo}, nil
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
		subnetSpec, err := getNetworkSubnetSpec(spec)
		if err != nil {
			return nil, err
		}
		subnetSpecs = append(subnetSpecs, subnetSpec)
	}

	s.log.Info("waiting for local cluster readiness")

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
		defer cancel()
		err := s.network.CreateSubnets(ctx, subnetSpecs)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.log.Error("failed to create subnets", zap.Error(err))
			s.stopAndRemoveNetwork(err)
			return
		} else {
			s.updateClusterInfo()
		}
		s.log.Info("subnets created")
	}()

	clusterInfo, err := deepCopy(s.clusterInfo)
	if err != nil {
		return nil, err
	}
	return &rpcpb.CreateSubnetsResponse{ClusterInfo: clusterInfo}, nil
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

	if req.GetPluginDir() != "" {
		nodeFlags[config.PluginDirKey] = req.GetPluginDir()
	}

	nodeConfig := node.Config{
		Name:               req.Name,
		Flags:              nodeFlags,
		BinaryPath:         req.GetExecPath(),
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
		req.GetExecPath(),
		req.GetPluginDir(),
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

	if err := s.network.updateNodeInfo(); err != nil {
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

	if err := s.network.updateNodeInfo(); err != nil {
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

	rootDataDir := req.GetRootDataDir()
	if len(rootDataDir) == 0 {
		rootDataDir = os.TempDir()
	}
	rootDataDir = filepath.Join(rootDataDir, rootDataDirPrefix)
	var err error
	rootDataDir, err = utils.MkDirWithTimestamp(rootDataDir)
	if err != nil {
		return nil, err
	}

	pid := int32(os.Getpid())
	s.log.Info("starting", zap.Int32("pid", pid), zap.String("root-data-dir", rootDataDir))

	s.network, err = newLocalNetwork(localNetworkOptions{
		execPath:            req.GetExecPath(),
		pluginDir:           req.GetPluginDir(),
		rootDataDir:         rootDataDir,
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
		Pid:         pid,
		RootDataDir: rootDataDir,
	}

	// blocking load snapshot to soon get not found snapshot errors
	if err := s.network.LoadSnapshot(req.SnapshotName); err != nil {
		s.log.Warn("snapshot load failed to complete", zap.Error(err))
		s.stopAndRemoveNetwork(nil)
		return nil, err
	}

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitForHealthyTimeout)
		defer cancel()
		err := s.network.AwaitHealthyAndUpdateNetworkInfo(ctx)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.log.Warn("snapshot load failed to complete. stopping network and cleaning up network", zap.Error(err))
			s.stopAndRemoveNetwork(err)
			return
		} else {
			s.updateClusterInfo()
		}
		s.log.Info("network healthy")
	}()

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

	snapshotPath, err := s.network.nw.SaveSnapshot(ctx, req.SnapshotName)
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

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	if err := s.network.nw.RemoveSnapshot(req.SnapshotName); err != nil {
		s.log.Warn("snapshot remove failed to complete", zap.Error(err))
		return nil, err
	}
	return &rpcpb.RemoveSnapshotResponse{}, nil
}

func (s *server) GetSnapshotNames(context.Context, *rpcpb.GetSnapshotNamesRequest) (*rpcpb.GetSnapshotNamesResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.log.Info("GetSnapshotNames")

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	snapshotNames, err := s.network.nw.GetSnapshotNames()
	if err != nil {
		return nil, err
	}
	return &rpcpb.GetSnapshotNamesResponse{SnapshotNames: snapshotNames}, nil
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
		if err := utils.CheckPluginPaths(
			filepath.Join(pluginDir, vmID.String()),
			spec.Genesis,
		); err != nil {
			return network.BlockchainSpec{}, err
		}
	}

	genesisBytes, err := os.ReadFile(spec.Genesis)
	if err != nil {
		return network.BlockchainSpec{}, err
	}

	var chainConfigBytes []byte
	if spec.ChainConfig != "" {
		chainConfigBytes, err = os.ReadFile(spec.ChainConfig)
		if err != nil {
			return network.BlockchainSpec{}, err
		}
	}

	var networkUpgradeBytes []byte
	if spec.NetworkUpgrade != "" {
		networkUpgradeBytes, err = os.ReadFile(spec.NetworkUpgrade)
		if err != nil {
			return network.BlockchainSpec{}, err
		}
	}

	var subnetConfigBytes []byte
	if spec.SubnetConfig != "" {
		subnetConfigBytes, err = os.ReadFile(spec.SubnetConfig)
		if err != nil {
			return network.BlockchainSpec{}, err
		}
	}

	perNodeChainConfig := map[string][]byte{}
	if spec.PerNodeChainConfig != "" {
		perNodeChainConfigBytes, err := os.ReadFile(spec.PerNodeChainConfig)
		if err != nil {
			return network.BlockchainSpec{}, err
		}

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
	return network.BlockchainSpec{
		VMName:             vmName,
		Genesis:            genesisBytes,
		ChainConfig:        chainConfigBytes,
		NetworkUpgrade:     networkUpgradeBytes,
		SubnetConfig:       subnetConfigBytes,
		SubnetID:           spec.SubnetId,
		BlockchainAlias:    spec.BlockchainAlias,
		PerNodeChainConfig: perNodeChainConfig,
	}, nil
}

func getNetworkSubnetSpec(
	spec *rpcpb.SubnetSpec,
) (network.SubnetSpec, error) {
	var subnetConfigBytes []byte
	var err error
	if spec.SubnetConfig != "" {
		subnetConfigBytes, err = os.ReadFile(spec.SubnetConfig)
		if err != nil {
			return network.SubnetSpec{}, err
		}
	}
	return network.SubnetSpec{
		Participants: spec.Participants,
		SubnetConfig: subnetConfigBytes,
	}, nil
}
