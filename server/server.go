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

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/snow/networking/router"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
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
	cfg Config
	log logging.Logger

	rootCtx   context.Context
	closeOnce sync.Once
	closed    chan struct{}

	ln               net.Listener
	gRPCServer       *grpc.Server
	gRPCRegisterOnce sync.Once

	gwMux    *runtime.ServeMux
	gwServer *http.Server

	mu          *sync.RWMutex
	clusterInfo *rpcpb.ClusterInfo
	network     *localNetwork
	asyncErrCh  chan error

	rpcpb.UnimplementedPingServiceServer
	rpcpb.UnimplementedControlServiceServer
}

var (
	ErrInvalidVMName          = errors.New("invalid VM name")
	ErrInvalidPort            = errors.New("invalid port")
	ErrClosed                 = errors.New("server closed")
	ErrNotEnoughNodesForStart = errors.New("not enough nodes specified for start")
	ErrAlreadyBootstrapped    = errors.New("already bootstrapped")
	ErrNotBootstrapped        = errors.New("not bootstrapped")
	ErrNodeNotFound           = errors.New("node not found")
	ErrPeerNotFound           = errors.New("peer not found")
	ErrStatusCanceled         = errors.New("gRPC stream status canceled")
	ErrNoBlockchainSpec       = errors.New("no blockchain spec was provided")
)

const (
	// RPCVersion should be bumped anytime changes are made which require
	// the RPC client to upgrade to latest RPC server to be compatible
	RPCVersion   uint32 = 1
	MinNodes     uint32 = 1
	DefaultNodes uint32 = 5
	stopTimeout         = 2 * time.Second

	rootDataDirPrefix = "network-runner-root-data"
)

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

	ln, err := net.Listen("tcp", cfg.Port)
	if err != nil {
		return nil, err
	}
	srv := &server{
		cfg: cfg,
		log: log,

		closed: make(chan struct{}),

		ln:         ln,
		gRPCServer: grpc.NewServer(),

		mu:         new(sync.RWMutex),
		asyncErrCh: make(chan error, 1),
	}
	if !cfg.GwDisabled {
		srv.gwMux = runtime.NewServeMux()
		srv.gwServer = &http.Server{ //nolint // TODO add ReadHeaderTimeout
			Addr:    cfg.GwPort,
			Handler: srv.gwMux,
		}
	}

	return srv, nil
}

// Blocking call until server listeners return.
func (s *server) Run(rootCtx context.Context) (err error) {
	s.rootCtx = rootCtx
	s.gRPCRegisterOnce.Do(func() {
		rpcpb.RegisterPingServiceServer(s.gRPCServer, s)
		rpcpb.RegisterControlServiceServer(s.gRPCServer, s)
	})

	gRPCErrc := make(chan error)
	go func() {
		s.log.Info("serving gRPC server", zap.String("port", s.cfg.Port))
		gRPCErrc <- s.gRPCServer.Serve(s.ln)
	}()

	gwErrc := make(chan error)
	if s.cfg.GwDisabled {
		s.log.Info("gRPC gateway server is disabled")
	} else {
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
				gwErrc <- err
				return
			}
			defer gwConn.Close()

			if err := rpcpb.RegisterPingServiceHandler(rootCtx, s.gwMux, gwConn); err != nil {
				gwErrc <- err
				return
			}
			if err := rpcpb.RegisterControlServiceHandler(rootCtx, s.gwMux, gwConn); err != nil {
				gwErrc <- err
				return
			}

			s.log.Info("serving gRPC gateway", zap.String("port", s.cfg.GwPort))
			gwErrc <- s.gwServer.ListenAndServe()
		}()
	}

	select {
	case <-rootCtx.Done():
		s.log.Warn("root context is done")

		if !s.cfg.GwDisabled {
			s.log.Warn("closed gRPC gateway server", zap.Error(s.gwServer.Close()))
			<-gwErrc
		}

		s.gRPCServer.Stop()
		s.log.Warn("closed gRPC server")
		<-gRPCErrc
		s.log.Warn("gRPC terminated")

	case err = <-gRPCErrc:
		s.log.Warn("gRPC server failed", zap.Error(err))

		if !s.cfg.GwDisabled {
			s.log.Warn("closed gRPC gateway server", zap.Error(s.gwServer.Close()))
			<-gwErrc
		}

	case err = <-gwErrc: // if disabled, this will never be selected
		s.log.Warn("gRPC gateway server failed", zap.Error(err))
		s.gRPCServer.Stop()
		s.log.Warn("closed gRPC server")
		<-gRPCErrc
	}

	if s.network != nil {
		stopCtx, stopCtxCancel := context.WithTimeout(context.Background(), stopTimeout)
		defer stopCtxCancel()
		s.network.stop(stopCtx)
		s.log.Warn("network stopped")
	}

	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return err
}

func (s *server) Ping(context.Context, *rpcpb.PingRequest) (*rpcpb.PingResponse, error) {
	s.log.Debug("received ping request")
	return &rpcpb.PingResponse{Pid: int32(os.Getpid())}, nil
}

const defaultStartTimeout = 5 * time.Minute

func (s *server) RPCVersion(context.Context, *rpcpb.RPCVersionRequest) (*rpcpb.RPCVersionResponse, error) {
	s.log.Debug("RPCVersion")

	return &rpcpb.RPCVersionResponse{Version: RPCVersion}, nil
}

func (s *server) Start(ctx context.Context, req *rpcpb.StartRequest) (*rpcpb.StartResponse, error) {
	// if timeout is too small or not set, default to 5-min
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < defaultStartTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultStartTimeout)
		_ = cancel // don't call since "start" is async, "curl" may not specify timeout
		s.log.Info("received start request with default timeout", zap.String("timeout", defaultStartTimeout.String()))
	} else {
		s.log.Info("received start request with existing timeout", zap.String("timeout", deadline.String()))
	}

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

	// If [network] is already populated, the network has already been started.
	if s.getNetwork() != nil {
		return nil, ErrAlreadyBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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

	s.clusterInfo = &rpcpb.ClusterInfo{
		Pid:         pid,
		RootDataDir: rootDataDir,
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

	if len(customNodeConfigs) > 0 {
		s.log.Warn("custom node configs have been provided; ignoring the 'number-of-nodes' parameter and setting it to:", zap.Int("number-of-nodes", len(customNodeConfigs)))
		numNodes = uint32(len(customNodeConfigs))
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
		s.network = nil
		return nil, err
	}

	if err := s.network.start(); err != nil {
		s.log.Warn("start failed to complete", zap.Error(err))
		s.network = nil
		return nil, err
	}

	// start non-blocking to install local cluster + custom chains (if applicable)
	// the user is expected to poll cluster status
	readyCh := make(chan struct{})
	go s.network.startWait(ctx, chainSpecs, readyCh)

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		s.waitChAndUpdateClusterInfo("local cluster", readyCh, false)
		s.waitChAndUpdateClusterInfo("custom chains", readyCh, true)
	}()

	return &rpcpb.StartResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) waitChAndUpdateClusterInfo(msg string, readyCh chan struct{}, updateCustomVmsInfo bool) {
	if s.getNetwork() == nil {
		return
	}
	s.log.Info(fmt.Sprintf("waiting for %s readiness", msg))
	select {
	case <-s.closed:
		return
	case <-s.getNetwork().stopCh:
		return
	case serr := <-s.getNetwork().startErrCh:
		s.mu.Lock()
		s.log.Warn("async call failed to complete", zap.String("async-call", msg), zap.Error(serr))
		stopCtx, stopCtxCancel := context.WithTimeout(context.Background(), stopTimeout)
		s.network.stop(stopCtx)
		stopCtxCancel()
		s.network = nil
		s.asyncErrCh <- serr
		s.mu.Unlock()
	case <-readyCh:
		s.mu.Lock()
		s.clusterInfo.Healthy = true
		s.clusterInfo.NodeNames = s.network.nodeNames
		s.clusterInfo.NodeInfos = s.network.nodeInfos
		if updateCustomVmsInfo {
			s.clusterInfo.CustomChainsHealthy = true
			s.clusterInfo.CustomChains = make(map[string]*rpcpb.CustomChainInfo)
			for chainID, chainInfo := range s.network.customChainIDToInfo {
				s.clusterInfo.CustomChains[chainID.String()] = chainInfo.info
			}
			s.clusterInfo.Subnets = s.network.subnets
		}
		s.log.Info(fmt.Sprintf("%s ready", msg))
		s.mu.Unlock()
	}
}

// wait until some of this conditions is met:
// - timeout expires
// - network operation terminates with error
// - network operation terminates successfully by setting CustomChainsHealthy
func (s *server) WaitForHealthy(ctx context.Context, _ *rpcpb.WaitForHealthyRequest) (*rpcpb.WaitForHealthyResponse, error) {
	s.log.Debug("WaitForHealthy")
	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var err error
	continueLoop := true
	for continueLoop {
		if s.clusterInfo.CustomChainsHealthy {
			break
		}
		select {
		case err = <-s.asyncErrCh:
			continueLoop = false
		case <-ctx.Done():
			continueLoop = false
			err = ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return &rpcpb.WaitForHealthyResponse{ClusterInfo: s.getClusterInfo()}, err
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

func (s *server) CreateBlockchains(
	ctx context.Context,
	req *rpcpb.CreateBlockchainsRequest,
) (*rpcpb.CreateBlockchainsResponse, error) {
	// if timeout is too small or not set, default to 5-min
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < defaultStartTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultStartTimeout)
		_ = cancel // don't call since "start" is async, "curl" may not specify timeout
		s.log.Info("received start request with default timeout", zap.String("timeout", defaultStartTimeout.String()))
	} else {
		s.log.Info("received start request with existing timeout", zap.String("timeout", deadline.String()))
	}

	s.log.Debug("CreateBlockchains")

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	if len(req.GetBlockchainSpecs()) == 0 {
		return nil, ErrNoBlockchainSpec
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	chainSpecs := []network.BlockchainSpec{}
	for _, spec := range req.GetBlockchainSpecs() {
		chainSpec, err := getNetworkBlockchainSpec(s.log, spec, false, s.network.pluginDir)
		if err != nil {
			return nil, err
		}
		chainSpecs = append(chainSpecs, chainSpec)
	}

	// check that defined subnets exist
	subnetsMap := map[string]struct{}{}
	for _, subnet := range s.clusterInfo.Subnets {
		subnetsMap[subnet] = struct{}{}
	}
	for _, chainSpec := range chainSpecs {
		if chainSpec.SubnetID != nil {
			_, ok := subnetsMap[*chainSpec.SubnetID]
			if !ok {
				return nil, fmt.Errorf("subnet id %q does not exits", *chainSpec.SubnetID)
			}
		}
	}

	// if there will be a restart, network will not be healthy
	// until finishing
	for _, chainSpec := range chainSpecs {
		if chainSpec.SubnetID == nil {
			s.clusterInfo.Healthy = false
		}
	}

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	// start non-blocking to install custom chains (if applicable)
	// the user is expected to poll cluster status
	readyCh := make(chan struct{})
	go s.network.createBlockchains(ctx, chainSpecs, readyCh)

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		s.waitChAndUpdateClusterInfo("custom chains", readyCh, true)
	}()

	return &rpcpb.CreateBlockchainsResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) CreateSubnets(ctx context.Context, req *rpcpb.CreateSubnetsRequest) (*rpcpb.CreateSubnetsResponse, error) {
	// if timeout is too small or not set, default to 5-min
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < defaultStartTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultStartTimeout)
		_ = cancel // don't call since "start" is async, "curl" may not specify timeout
		s.log.Info("received start request with default timeout", zap.String("timeout", defaultStartTimeout.String()))
	} else {
		s.log.Info("received start request with existing timeout", zap.String("timeout", deadline.String()))
	}

	s.log.Debug("CreateSubnets", zap.Uint32("num-subnets", req.GetNumSubnets()))

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	// default behaviour without args is to create one subnet
	numSubnets := req.GetNumSubnets()
	if numSubnets == 0 {
		numSubnets = 1
	}

	s.log.Info("waiting for local cluster readiness")
	if err := s.getNetwork().waitForLocalClusterReady(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomChainsHealthy = false

	// start non-blocking to add subnets
	// the user is expected to poll cluster status
	readyCh := make(chan struct{})
	go s.network.createSubnets(ctx, numSubnets, readyCh)

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		s.waitChAndUpdateClusterInfo("custom chains", readyCh, true)
	}()

	return &rpcpb.CreateSubnetsResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) Health(ctx context.Context, _ *rpcpb.HealthRequest) (*rpcpb.HealthResponse, error) {
	s.log.Debug("Health")

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	s.log.Info("waiting for local cluster readiness")
	if err := s.getNetwork().waitForLocalClusterReady(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.clusterInfo.NodeNames = s.network.nodeNames
	s.clusterInfo.NodeInfos = s.network.nodeInfos
	s.clusterInfo.Healthy = true

	return &rpcpb.HealthResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) URIs(context.Context, *rpcpb.URIsRequest) (*rpcpb.URIsResponse, error) {
	s.log.Debug("URIs")

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	info := s.getClusterInfo()
	uris := make([]string, 0, len(info.NodeInfos))
	for _, i := range info.NodeInfos {
		uris = append(uris, i.Uri)
	}
	sort.Strings(uris)
	return &rpcpb.URIsResponse{Uris: uris}, nil
}

func (s *server) Status(context.Context, *rpcpb.StatusRequest) (*rpcpb.StatusResponse, error) {
	s.log.Debug("Status")

	if s.getNetwork() == nil {
		return &rpcpb.StatusResponse{ClusterInfo: s.getClusterInfo()}, ErrNotBootstrapped
	}

	return &rpcpb.StatusResponse{ClusterInfo: s.getClusterInfo()}, nil
}

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

	errc := make(chan error, 1)
	go func() {
		rerr := s.recvLoop(stream)
		if rerr != nil {
			if isClientCanceled(stream.Context().Err(), rerr) {
				s.log.Warn("failed to receive status request from gRPC stream due to client cancellation", zap.Error(rerr))
			} else {
				s.log.Warn("failed to receive status request from gRPC stream", zap.Error(rerr))
			}
		}
		errc <- rerr
	}()

	select {
	case err = <-errc:
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

func (s *server) sendLoop(stream rpcpb.ControlService_StreamStatusServer, interval time.Duration) {
	s.log.Info("start status send loop")

	tc := time.NewTicker(1)
	defer tc.Stop()

	for {
		select {
		case <-s.rootCtx.Done():
			return
		case <-s.closed:
			return
		case <-tc.C:
			tc.Reset(interval)
		}

		s.log.Debug("sending cluster info")
		if err := stream.Send(&rpcpb.StreamStatusResponse{ClusterInfo: s.getClusterInfo()}); err != nil {
			if isClientCanceled(stream.Context().Err(), err) {
				s.log.Debug("client stream canceled", zap.Error(err))
				return
			}
			s.log.Warn("failed to send an event", zap.Error(err))
			return
		}
	}
}

func (s *server) recvLoop(stream rpcpb.ControlService_StreamStatusServer) error {
	s.log.Info("start status receive loop")

	for {
		select {
		case <-s.rootCtx.Done():
			return s.rootCtx.Err()
		case <-s.closed:
			return ErrClosed
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
	s.log.Debug("AddNode", zap.String("name", req.Name))

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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

	if err := s.network.updateNodeInfo(); err != nil {
		return nil, err
	}

	s.clusterInfo.NodeNames = s.network.nodeNames
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	return &rpcpb.AddNodeResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) RemoveNode(ctx context.Context, req *rpcpb.RemoveNodeRequest) (*rpcpb.RemoveNodeResponse, error) {
	s.log.Debug("RemoveNode", zap.String("name", req.Name))

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.network.nw.RemoveNode(ctx, req.Name); err != nil {
		return nil, err
	}

	if err := s.network.updateNodeInfo(); err != nil {
		return nil, err
	}

	s.clusterInfo.NodeNames = s.network.nodeNames
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	return &rpcpb.RemoveNodeResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) RestartNode(ctx context.Context, req *rpcpb.RestartNodeRequest) (*rpcpb.RestartNodeResponse, error) {
	s.log.Debug("RestartNode", zap.String("name", req.Name))

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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

	if err := s.network.updateNodeInfo(); err != nil {
		return nil, err
	}

	s.clusterInfo.NodeNames = s.network.nodeNames
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	return &rpcpb.RestartNodeResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) Stop(ctx context.Context, _ *rpcpb.StopRequest) (*rpcpb.StopResponse, error) {
	s.log.Debug("Stop")

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.network.stop(ctx)
	s.network = nil

	info := s.clusterInfo
	if info == nil {
		info = &rpcpb.ClusterInfo{}
	}
	info.Healthy = false

	return &rpcpb.StopResponse{ClusterInfo: info}, nil
}

var _ router.InboundHandler = &loggingInboundHandler{}

type loggingInboundHandler struct {
	nodeName string
	log      logging.Logger
}

func (lh *loggingInboundHandler) HandleInbound(_ context.Context, m message.InboundMessage) {
	lh.log.Debug("inbound handler received a message", zap.String("message", m.Op().String()), zap.String("node-name", lh.nodeName))
}

func (s *server) AttachPeer(ctx context.Context, req *rpcpb.AttachPeerRequest) (*rpcpb.AttachPeerResponse, error) {
	s.log.Debug("AttachPeer")

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	node, err := s.network.nw.GetNode(req.NodeName)
	if err != nil {
		return nil, err
	}

	lh := &loggingInboundHandler{nodeName: req.NodeName, log: s.log}
	newPeer, err := node.AttachPeer(ctx, lh)
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

	return &rpcpb.AttachPeerResponse{ClusterInfo: s.clusterInfo, AttachedPeerInfo: peerInfo}, nil
}

func (s *server) SendOutboundMessage(ctx context.Context, req *rpcpb.SendOutboundMessageRequest) (*rpcpb.SendOutboundMessageResponse, error) {
	s.log.Debug("SendOutboundMessage")

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	node, err := s.getNetwork().nw.GetNode(req.NodeName)
	if err != nil {
		return nil, err
	}

	sent, err := node.SendOutboundMessage(ctx, req.PeerId, req.Bytes, req.Op)
	return &rpcpb.SendOutboundMessageResponse{Sent: sent}, err
}

func (s *server) LoadSnapshot(ctx context.Context, req *rpcpb.LoadSnapshotRequest) (*rpcpb.LoadSnapshotResponse, error) {
	// if timeout is too small or not set, default to 5-min
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < defaultStartTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultStartTimeout)
		_ = cancel // don't call since "start" is async, "curl" may not specify timeout
		s.log.Info("received start request with default timeout", zap.String("timeout", defaultStartTimeout.String()))
	} else {
		s.log.Info("received start request with existing timeout", zap.String("timeout", deadline.String()))
	}

	// If [network] is already populated, the network has already been started.
	if s.getNetwork() != nil {
		return nil, ErrAlreadyBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		pid = int32(os.Getpid())
		err error
	)

	rootDataDir := req.GetRootDataDir()
	if len(rootDataDir) == 0 {
		rootDataDir = os.TempDir()
	}
	rootDataDir = filepath.Join(rootDataDir, rootDataDirPrefix)
	rootDataDir, err = utils.MkDirWithTimestamp(rootDataDir)
	if err != nil {
		return nil, err
	}

	s.clusterInfo = &rpcpb.ClusterInfo{
		Pid:         pid,
		RootDataDir: rootDataDir,
	}

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
		s.network = nil
		return nil, err
	}

	// blocking load snapshot to soon get not found snapshot errors
	if err := s.network.loadSnapshot(ctx, req.SnapshotName); err != nil {
		s.log.Warn("snapshot load failed to complete", zap.Error(err))
		s.network = nil
		return nil, err
	}

	// start non-blocking wait to load snapshot results
	// the user is expected to poll cluster status
	readyCh := make(chan struct{})
	go s.network.loadSnapshotWait(ctx, readyCh)

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		s.waitChAndUpdateClusterInfo("local cluster", readyCh, true)
	}()

	return &rpcpb.LoadSnapshotResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) SaveSnapshot(ctx context.Context, req *rpcpb.SaveSnapshotRequest) (*rpcpb.SaveSnapshotResponse, error) {
	s.log.Info("SaveSnapshot", zap.String("snapshot-name", req.SnapshotName))

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	snapshotPath, err := s.network.nw.SaveSnapshot(ctx, req.SnapshotName)
	if err != nil {
		s.log.Warn("snapshot save failed to complete", zap.Error(err))
		return nil, err
	}
	s.network = nil

	return &rpcpb.SaveSnapshotResponse{SnapshotPath: snapshotPath}, nil
}

func (s *server) RemoveSnapshot(_ context.Context, req *rpcpb.RemoveSnapshotRequest) (*rpcpb.RemoveSnapshotResponse, error) {
	s.log.Info("RemoveSnapshot", zap.String("snapshot-name", req.SnapshotName))

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	if err := s.getNetwork().nw.RemoveSnapshot(req.SnapshotName); err != nil {
		s.log.Warn("snapshot remove failed to complete", zap.Error(err))
		return nil, err
	}
	return &rpcpb.RemoveSnapshotResponse{}, nil
}

func (s *server) GetSnapshotNames(context.Context, *rpcpb.GetSnapshotNamesRequest) (*rpcpb.GetSnapshotNamesResponse, error) {
	s.log.Info("GetSnapshotNames")

	if s.getNetwork() == nil {
		return nil, ErrNotBootstrapped
	}

	snapshotNames, err := s.getNetwork().nw.GetSnapshotNames()
	if err != nil {
		return nil, err
	}
	return &rpcpb.GetSnapshotNamesResponse{SnapshotNames: snapshotNames}, nil
}

func (s *server) getClusterInfo() *rpcpb.ClusterInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clusterInfo
}

func (s *server) getNetwork() *localNetwork {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.network
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
