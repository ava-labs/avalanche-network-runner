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
	"io/fs"
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
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/snow/networking/router"
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
}

type Server interface {
	Run(rootCtx context.Context) error
}

type server struct {
	cfg Config

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

	rpcpb.UnimplementedPingServiceServer
	rpcpb.UnimplementedControlServiceServer
}

var (
	ErrInvalidVMName                      = errors.New("invalid VM name")
	ErrInvalidPort                        = errors.New("invalid port")
	ErrClosed                             = errors.New("server closed")
	ErrPluginDirEmptyButCustomVMsNotEmpty = errors.New("empty plugin-dir but non-empty custom VMs")
	ErrPluginDirNonEmptyButCustomVMsEmpty = errors.New("non-empty plugin-dir but empty custom VM")
	ErrNotEnoughNodesForStart             = errors.New("not enough nodes specified for start")
	ErrAlreadyBootstrapped                = errors.New("already bootstrapped")
	ErrNotBootstrapped                    = errors.New("not bootstrapped")
	ErrNodeNotFound                       = errors.New("node not found")
	ErrPeerNotFound                       = errors.New("peer not found")
	ErrStatusCanceled                     = errors.New("gRPC stream status canceled")
)

const (
	MinNodes            uint32 = 1
	DefaultNodes        uint32 = 5
	stopOnSignalTimeout        = 2 * time.Second

	rootDataDirPrefix = "network-runner-root-data"
)

func New(cfg Config) (Server, error) {
	if cfg.Port == "" || cfg.GwPort == "" {
		return nil, ErrInvalidPort
	}

	ln, err := net.Listen("tcp", cfg.Port)
	if err != nil {
		return nil, err
	}
	srv := &server{
		cfg: cfg,

		closed: make(chan struct{}),

		ln:         ln,
		gRPCServer: grpc.NewServer(),

		mu: new(sync.RWMutex),
	}
	if !cfg.GwDisabled {
		srv.gwMux = runtime.NewServeMux()
		srv.gwServer = &http.Server{
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
		zap.L().Info("serving gRPC server", zap.String("port", s.cfg.Port))
		gRPCErrc <- s.gRPCServer.Serve(s.ln)
	}()

	gwErrc := make(chan error)
	if s.cfg.GwDisabled {
		zap.L().Info("gRPC gateway server is disabled")
	} else {
		go func() {
			zap.L().Info("dialing gRPC server for gRPC gateway", zap.String("port", s.cfg.Port))
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

			zap.L().Info("serving gRPC gateway", zap.String("port", s.cfg.GwPort))
			gwErrc <- s.gwServer.ListenAndServe()
		}()
	}

	select {
	case <-rootCtx.Done():
		zap.L().Warn("root context is done")

		if !s.cfg.GwDisabled {
			zap.L().Warn("closed gRPC gateway server", zap.Error(s.gwServer.Close()))
			<-gwErrc
		}

		s.gRPCServer.Stop()
		zap.L().Warn("closed gRPC server")
		<-gRPCErrc
		zap.L().Warn("gRPC terminated")

	case err = <-gRPCErrc:
		zap.L().Warn("gRPC server failed", zap.Error(err))
		if !s.cfg.GwDisabled {
			zap.L().Warn("closed gRPC gateway server", zap.Error(s.gwServer.Close()))
			<-gwErrc
		}

	case err = <-gwErrc: // if disabled, this will never be selected
		zap.L().Warn("gRPC gateway server failed", zap.Error(err))
		s.gRPCServer.Stop()
		zap.L().Warn("closed gRPC server")
		<-gRPCErrc
	}

	if s.network != nil {
		stopCtx, stopCtxCancel := context.WithTimeout(context.Background(), stopOnSignalTimeout)
		defer stopCtxCancel()
		s.network.stop(stopCtx)
		zap.L().Warn("network stopped")
	}

	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return err
}

func (s *server) Ping(ctx context.Context, req *rpcpb.PingRequest) (*rpcpb.PingResponse, error) {
	zap.L().Debug("received ping request")
	return &rpcpb.PingResponse{Pid: int32(os.Getpid())}, nil
}

const defaultStartTimeout = 5 * time.Minute

func (s *server) Start(ctx context.Context, req *rpcpb.StartRequest) (*rpcpb.StartResponse, error) {
	// if timeout is too small or not set, default to 5-min
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < defaultStartTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultStartTimeout)
		_ = cancel // don't call since "start" is async, "curl" may not specify timeout
		zap.L().Info("received start request with default timeout", zap.String("timeout", defaultStartTimeout.String()))
	} else {
		zap.L().Info("received start request with existing timeout", zap.String("deadline", deadline.String()))
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
	pluginDir := ""
	if req.GetPluginDir() != "" {
		pluginDir = req.GetPluginDir()
	}
	if pluginDir == "" {
		pluginDir = filepath.Join(filepath.Dir(req.GetExecPath()), "plugins")
	}
	chainSpecs := []network.BlockchainSpec{}
	if len(req.GetCustomVms()) > 0 {
		zap.L().Info("plugin dir", zap.String("plugin-dir", pluginDir))
		for vmName, vmGenesisFilePath := range req.GetCustomVms() {
			zap.L().Info("checking custom VM ID before installation", zap.String("vm-id", vmName))
			vmID, err := utils.VMID(vmName)
			if err != nil {
				zap.L().Warn("failed to convert VM name to VM ID",
					zap.String("vm-name", vmName),
					zap.Error(err),
				)
				return nil, ErrInvalidVMName
			}
			if err := utils.CheckPluginPaths(
				filepath.Join(pluginDir, vmID.String()),
				vmGenesisFilePath,
			); err != nil {
				return nil, err
			}
			b, err := os.ReadFile(vmGenesisFilePath)
			if err != nil {
				return nil, err
			}
			chainSpecs = append(chainSpecs, network.BlockchainSpec{
				VmName:  vmName,
				Genesis: b,
			})
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// If [clusterInfo] is already populated, the server has already been started.
	if s.clusterInfo != nil {
		return nil, ErrAlreadyBootstrapped
	}

	var (
		execPath           = req.GetExecPath()
		numNodes           = req.GetNumNodes()
		whitelistedSubnets = req.GetWhitelistedSubnets()
		rootDataDir        = req.GetRootDataDir()
		pid                = int32(os.Getpid())
		globalNodeConfig   = req.GetGlobalNodeConfig()
		customNodeConfigs  = req.GetCustomNodeConfigs()
		err                error
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
		Healthy:     false,
	}

	zap.L().Info("starting",
		zap.String("execPath", execPath),
		zap.Uint32("numNodes", numNodes),
		zap.String("whitelistedSubnets", whitelistedSubnets),
		zap.Int32("pid", pid),
		zap.String("rootDataDir", rootDataDir),
		zap.String("pluginDir", pluginDir),
		zap.Any("chainConfigs", req.ChainConfigs),
		zap.String("globalNodeConfig", globalNodeConfig),
	)

	if s.network != nil {
		return nil, ErrAlreadyBootstrapped
	}

	if len(customNodeConfigs) > 0 {
		zap.L().Warn("custom node configs have been provided; ignoring the 'number-of-nodes' parameter and setting it to", zap.Int("numNodes", len(customNodeConfigs)))
		numNodes = uint32(len(customNodeConfigs))
	}

	s.network, err = newLocalNetwork(localNetworkOptions{
		execPath:            execPath,
		rootDataDir:         rootDataDir,
		numNodes:            numNodes,
		whitelistedSubnets:  whitelistedSubnets,
		redirectNodesOutput: s.cfg.RedirectNodesOutput,
		pluginDir:           pluginDir,
		globalNodeConfig:    globalNodeConfig,
		customNodeConfigs:   customNodeConfigs,
		chainConfigs:        req.ChainConfigs,

		// to block racey restart
		// "s.network.start" runs asynchronously
		// so it would not deadlock with the acquired lock
		// in this "Start" method
		restartMu: s.mu,

		snapshotsDir: s.cfg.SnapshotsDir,
	})
	if err != nil {
		s.network = nil
		s.clusterInfo = nil
		return nil, err
	}

	if err := s.network.start(); err != nil {
		s.network = nil
		s.clusterInfo = nil
		return nil, err
	}

	// start non-blocking to install local cluster + custom VMs (if applicable)
	// the user is expected to poll cluster status
	readyCh := make(chan struct{})
	go s.network.startWait(ctx, chainSpecs, readyCh)

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		s.waitChAndUpdateClusterInfo("waiting for local cluster readiness", readyCh, false)
		if len(req.GetCustomVms()) == 0 {
			zap.L().Info("no custom VM installation request, skipping its readiness check")
		} else {
			s.waitChAndUpdateClusterInfo("waiting for custom VMs readiness", readyCh, true)
		}
	}()

	return &rpcpb.StartResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) waitChAndUpdateClusterInfo(waitMsg string, readyCh chan struct{}, updateCustomVmsInfo bool) {
	zap.L().Info(waitMsg)
	select {
	case <-s.closed:
		return
	case <-s.network.stopCh:
		return
	case serr := <-s.network.startErrCh:
		// TODO: decide what to do here, general failure cause network stop()?
		// maybe try decide if operation was partial (undesired network, fail)
		// or was not stated (preconditions check, continue)
		zap.L().Warn("async call failed to complete", zap.String("op", waitMsg), zap.Error(serr))
		panic(serr)
	case <-readyCh:
		s.mu.Lock()
		s.clusterInfo.Healthy = true
		s.clusterInfo.NodeNames = s.network.nodeNames
		s.clusterInfo.NodeInfos = s.network.nodeInfos
		if updateCustomVmsInfo {
			s.clusterInfo.CustomVmsHealthy = true
			s.clusterInfo.CustomVms = make(map[string]*rpcpb.CustomVmInfo)
			for blockchainID, vmInfo := range s.network.customVMBlockchainIDToInfo {
				s.clusterInfo.CustomVms[blockchainID.String()] = vmInfo.info
			}
			s.clusterInfo.Subnets = s.network.subnets
		}
		s.mu.Unlock()
	}
}

func (s *server) CreateBlockchains(ctx context.Context, req *rpcpb.CreateBlockchainsRequest) (*rpcpb.CreateBlockchainsResponse, error) {
	// if timeout is too small or not set, default to 5-min
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < defaultStartTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultStartTimeout)
		_ = cancel // don't call since "start" is async, "curl" may not specify timeout
		zap.L().Info("received start request with default timeout", zap.String("timeout", defaultStartTimeout.String()))
	} else {
		zap.L().Info("received start request with existing timeout", zap.String("deadline", deadline.String()))
	}

	zap.L().Debug("CreateBlockchains")
	if info := s.getClusterInfo(); info == nil {
		return nil, ErrNotBootstrapped
	}

	if len(req.GetBlockchainSpecs()) == 0 {
		return nil, errors.New("no blockchain spec was provided")
	}

	chainSpecs := []network.BlockchainSpec{}
	for i := range req.GetBlockchainSpecs() {
		vmName := req.GetBlockchainSpecs()[i].VmName
		vmGenesisFilePath := req.GetBlockchainSpecs()[i].Genesis
		zap.L().Info("checking custom VM ID before installation", zap.String("vm-id", vmName))
		vmID, err := utils.VMID(vmName)
		if err != nil {
			zap.L().Warn("failed to convert VM name to VM ID",
				zap.String("vm-name", vmName),
				zap.Error(err),
			)
			return nil, ErrInvalidVMName
		}
		if err := utils.CheckPluginPaths(
			filepath.Join(s.network.pluginDir, vmID.String()),
			vmGenesisFilePath,
		); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(vmGenesisFilePath)
		if err != nil {
			return nil, err
		}
		chainSpecs = append(chainSpecs, network.BlockchainSpec{
			VmName:   vmName,
			Genesis:  b,
			SubnetId: req.GetBlockchainSpecs()[i].SubnetId,
		})
	}

	// check that defined subnets exist
	subnetsMap := map[string]struct{}{}
	for _, subnet := range s.clusterInfo.Subnets {
		subnetsMap[subnet] = struct{}{}
	}
	for _, chainSpec := range chainSpecs {
		if chainSpec.SubnetId != nil {
			_, ok := subnetsMap[*chainSpec.SubnetId]
			if !ok {
				return nil, fmt.Errorf("subnet id %q does not exits", *chainSpec.SubnetId)
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// if there will be a restart, network will not be healthy
	// until finishing
	for _, chainSpec := range chainSpecs {
		if chainSpec.SubnetId == nil {
			s.clusterInfo.Healthy = false
		}
	}

	s.clusterInfo.CustomVmsHealthy = false

	// start non-blocking to install custom VMs (if applicable)
	// the user is expected to poll cluster status
	readyCh := make(chan struct{})
	go s.network.createBlockchains(ctx, chainSpecs, readyCh)

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		s.waitChAndUpdateClusterInfo("waiting for custom VMs readiness", readyCh, true)
	}()

	return &rpcpb.CreateBlockchainsResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) CreateSubnets(ctx context.Context, req *rpcpb.CreateSubnetsRequest) (*rpcpb.CreateSubnetsResponse, error) {
	// if timeout is too small or not set, default to 5-min
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < defaultStartTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultStartTimeout)
		_ = cancel // don't call since "start" is async, "curl" may not specify timeout
		zap.L().Info("received start request with default timeout", zap.String("timeout", defaultStartTimeout.String()))
	} else {
		zap.L().Info("received start request with existing timeout", zap.String("deadline", deadline.String()))
	}

	zap.L().Debug("CreateSubnets", zap.Uint32("num-subnets", req.GetNumSubnets()))

	if info := s.getClusterInfo(); info == nil {
		return nil, ErrNotBootstrapped
	}

	// default behaviour without args is to create one subnet
	numSubnets := req.GetNumSubnets()
	if numSubnets == 0 {
		numSubnets = 1
	}

	zap.L().Info("waiting for local cluster readiness")
	if err := s.network.waitForLocalClusterReady(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.clusterInfo.Healthy = false
	s.clusterInfo.CustomVmsHealthy = false

	// start non-blocking to add subnets
	// the user is expected to poll cluster status
	readyCh := make(chan struct{})
	go s.network.createSubnets(ctx, numSubnets, readyCh)

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		s.waitChAndUpdateClusterInfo("waiting for custom VMs readiness", readyCh, true)
	}()

	return &rpcpb.CreateSubnetsResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) Health(ctx context.Context, req *rpcpb.HealthRequest) (*rpcpb.HealthResponse, error) {
	zap.L().Debug("health")
	if info := s.getClusterInfo(); info == nil {
		return nil, ErrNotBootstrapped
	}

	zap.L().Info("waiting for local cluster readiness")
	if err := s.network.waitForLocalClusterReady(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.clusterInfo.NodeNames = s.network.nodeNames
	s.clusterInfo.NodeInfos = s.network.nodeInfos
	s.clusterInfo.Healthy = true

	return &rpcpb.HealthResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) URIs(ctx context.Context, req *rpcpb.URIsRequest) (*rpcpb.URIsResponse, error) {
	zap.L().Debug("uris")
	info := s.getClusterInfo()
	if info == nil {
		return nil, ErrNotBootstrapped
	}
	uris := make([]string, 0, len(info.NodeInfos))
	for _, i := range info.NodeInfos {
		uris = append(uris, i.Uri)
	}
	sort.Strings(uris)
	return &rpcpb.URIsResponse{Uris: uris}, nil
}

func (s *server) Status(ctx context.Context, req *rpcpb.StatusRequest) (*rpcpb.StatusResponse, error) {
	zap.L().Debug("received status request")
	info := s.getClusterInfo()
	if info == nil {
		return nil, ErrNotBootstrapped
	}
	return &rpcpb.StatusResponse{ClusterInfo: info}, nil
}

func (s *server) StreamStatus(req *rpcpb.StreamStatusRequest, stream rpcpb.ControlService_StreamStatusServer) (err error) {
	zap.L().Info("received bootstrap status request")
	if s.getClusterInfo() == nil {
		return ErrNotBootstrapped
	}

	interval := time.Duration(req.PushInterval)

	// returns this method, then server closes the stream
	zap.L().Info("pushing status updates to the stream", zap.String("interval", interval.String()))
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
				zap.L().Warn("failed to receive status request from gRPC stream due to client cancellation", zap.Error(rerr))
			} else {
				zap.L().Warn("failed to receive status request from gRPC stream", zap.Error(rerr))
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
	zap.L().Info("start status send loop")

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

		zap.L().Debug("sending cluster info")
		if err := stream.Send(&rpcpb.StreamStatusResponse{ClusterInfo: s.getClusterInfo()}); err != nil {
			if isClientCanceled(stream.Context().Err(), err) {
				zap.L().Debug("client stream canceled", zap.Error(err))
				return
			}
			zap.L().Warn("failed to send an event", zap.Error(err))
			return
		}
	}
}

func (s *server) recvLoop(stream rpcpb.ControlService_StreamStatusServer) error {
	zap.L().Info("start status receive loop")

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
			zap.L().Debug("received EOF from client; returning to close the stream from server side")
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *server) AddNode(ctx context.Context, req *rpcpb.AddNodeRequest) (*rpcpb.AddNodeResponse, error) {
	zap.L().Debug("received add node request", zap.String("name", req.Name))

	if info := s.getClusterInfo(); info == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.network.nodeInfos[req.Name]; exists {
		return nil, fmt.Errorf("repeated node name %q", req.Name)
	}
	// fix if not given
	if req.StartRequest == nil {
		req.StartRequest = &rpcpb.StartRequest{}
	}
	// user can override bin path for this node...
	execPath := req.StartRequest.ExecPath
	if execPath == "" {
		// ...or use the same binary as the rest of the network
		execPath = s.network.execPath
	}
	_, err := os.Stat(execPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, utils.ErrNotExists
		}
		return nil, fmt.Errorf("failed to stat exec %q (%w)", execPath, err)
	}

	// as execPath can be provided by this function, we might need a new `build-dir`
	buildDir, err := getBuildDir(execPath, s.network.pluginDir)
	if err != nil {
		return nil, err
	}

	var globalConfig map[string]interface{}
	if req.StartRequest.GetGlobalNodeConfig() != "" {
		if err := json.Unmarshal([]byte(req.StartRequest.GetGlobalNodeConfig()), &globalConfig); err != nil {
			fmt.Printf("GLOBALNODECONFIG ERR %s %s\n", req.StartRequest.GetGlobalNodeConfig(), err)
			return nil, err
		}
	} else {
		globalConfig = map[string]interface{}{}
	}

	globalConfig[config.BuildDirKey] = buildDir

	nodeConfig := node.Config{
		Name:           req.Name,
		Flags:          globalConfig,
		BinaryPath:     execPath,
		RedirectStdout: s.cfg.RedirectNodesOutput,
		RedirectStderr: s.cfg.RedirectNodesOutput,
	}
	nodeConfig.ChainConfigFiles = map[string]string{}
	for k, v := range req.StartRequest.ChainConfigs {
		nodeConfig.ChainConfigFiles[k] = v
	}
	_, err = s.network.nw.AddNode(nodeConfig)
	if err != nil {
		fmt.Printf("ADDNODE ERR %#v %s\n", nodeConfig, err)
		return nil, err
	}
	fmt.Println("ADDNODE CALL END")
	if err := s.network.updateNodeInfo(); err != nil {
		return nil, err
	}

	return &rpcpb.AddNodeResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) RemoveNode(ctx context.Context, req *rpcpb.RemoveNodeRequest) (*rpcpb.RemoveNodeResponse, error) {
	zap.L().Debug("received remove node request", zap.String("name", req.Name))
	if info := s.getClusterInfo(); info == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.network.nodeInfos[req.Name]; !ok {
		return nil, ErrNodeNotFound
	}

	if err := s.network.nw.RemoveNode(ctx, req.Name); err != nil {
		return nil, err
	}

	zap.L().Info("waiting for local cluster readiness")
	if err := s.network.waitForLocalClusterReady(ctx); err != nil {
		return nil, err
	}

	s.clusterInfo.Healthy = true
	s.clusterInfo.NodeNames = s.network.nodeNames
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	return &rpcpb.RemoveNodeResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) RestartNode(ctx context.Context, req *rpcpb.RestartNodeRequest) (*rpcpb.RestartNodeResponse, error) {
	zap.L().Debug("received remove node request", zap.String("name", req.Name))
	if info := s.getClusterInfo(); info == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	nodeInfo, ok := s.network.nodeInfos[req.Name]
	if !ok {
		return nil, ErrNodeNotFound
	}

	node, err := s.network.nw.GetNode(req.Name)
	if err != nil {
		return nil, ErrNodeNotFound
	}
	nodeConfig := node.GetConfig()

	// use existing value if not specified
	if req.GetExecPath() != "" {
		nodeInfo.ExecPath = req.GetExecPath()
	}
	if req.GetWhitelistedSubnets() != "" {
		nodeInfo.WhitelistedSubnets = req.GetWhitelistedSubnets()
	}
	if req.GetRootDataDir() != "" {
		nodeInfo.DbDir = filepath.Join(req.GetRootDataDir(), req.Name, "db-dir")
	}

	buildDir, err := getBuildDir(nodeInfo.ExecPath, nodeInfo.PluginDir)
	if err != nil {
		return nil, err
	}

	nodeConfig.Flags[config.LogsDirKey] = nodeInfo.LogDir
	nodeConfig.Flags[config.DBPathKey] = nodeInfo.DbDir
	if buildDir != "" {
		nodeConfig.Flags[config.BuildDirKey] = buildDir
	}
	if nodeInfo.WhitelistedSubnets != "" {
		nodeConfig.Flags[config.WhitelistedSubnetsKey] = nodeInfo.WhitelistedSubnets
	}

	nodeConfig.BinaryPath = nodeInfo.ExecPath
	nodeConfig.RedirectStdout = s.cfg.RedirectNodesOutput
	nodeConfig.RedirectStderr = s.cfg.RedirectNodesOutput

	// now remove the node before restart
	zap.L().Info("removing the node")
	if err := s.network.nw.RemoveNode(ctx, req.Name); err != nil {
		return nil, err
	}

	// now adding the new node
	zap.L().Info("adding the node")
	if _, err := s.network.nw.AddNode(nodeConfig); err != nil {
		return nil, err
	}

	zap.L().Info("waiting for local cluster readiness")
	if err := s.network.waitForLocalClusterReady(ctx); err != nil {
		return nil, err
	}

	// update with the new config
	s.clusterInfo.NodeNames = s.network.nodeNames
	s.clusterInfo.NodeInfos = s.network.nodeInfos
	s.clusterInfo.Healthy = true

	return &rpcpb.RestartNodeResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) Stop(ctx context.Context, req *rpcpb.StopRequest) (*rpcpb.StopResponse, error) {
	zap.L().Debug("received stop request")

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.network == nil {
		return nil, ErrNotBootstrapped
	}

	info := s.clusterInfo
	if info == nil {
		info = &rpcpb.ClusterInfo{}
	}

	s.network.stop(ctx)
	s.network = nil
	s.clusterInfo = nil

	info.Healthy = false
	return &rpcpb.StopResponse{ClusterInfo: info}, nil
}

func (s *server) AttachPeer(ctx context.Context, req *rpcpb.AttachPeerRequest) (*rpcpb.AttachPeerResponse, error) {
	zap.L().Debug("received attach peer request")
	info := s.getClusterInfo()
	if info == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	node, err := s.network.nw.GetNode((req.NodeName))
	if err != nil {
		return nil, err
	}

	lh := &loggingInboundHandler{nodeName: req.NodeName}
	newPeer, err := node.AttachPeer(ctx, lh)
	if err != nil {
		return nil, err
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = newPeer.AwaitReady(cctx)
	cancel()
	if err != nil {
		return nil, err
	}
	newPeerID := newPeer.ID().String()

	zap.L().Debug("new peer is attached",
		zap.String("node-name", req.NodeName),
		zap.String("peer-id", newPeerID),
	)

	peers, ok := s.network.attachedPeers[req.NodeName]
	if !ok {
		peers = make(map[string]peer.Peer)
		peers[newPeerID] = newPeer
	} else {
		peers[newPeerID] = newPeer
	}
	s.network.attachedPeers[req.NodeName] = peers

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

	return &rpcpb.AttachPeerResponse{ClusterInfo: info, AttachedPeerInfo: peerInfo}, nil
}

var _ router.InboundHandler = &loggingInboundHandler{}

type loggingInboundHandler struct {
	nodeName string
}

func (lh *loggingInboundHandler) HandleInbound(m message.InboundMessage) {
	zap.L().Debug("inbound handler received a message",
		zap.String("node-name", lh.nodeName),
		zap.String("message-op", m.Op().String()),
	)
}

func (s *server) SendOutboundMessage(ctx context.Context, req *rpcpb.SendOutboundMessageRequest) (*rpcpb.SendOutboundMessageResponse, error) {
	zap.L().Debug("received send outbound message request")
	info := s.getClusterInfo()
	if info == nil {
		return nil, ErrNotBootstrapped
	}

	peers, ok := s.network.attachedPeers[req.NodeName]
	if !ok {
		return nil, ErrNodeNotFound
	}
	attachedPeer, ok := peers[req.PeerId]
	if !ok {
		return nil, ErrPeerNotFound
	}

	msg := message.NewTestMsg(message.Op(req.Op), req.Bytes, false)
	sent := attachedPeer.Send(ctx, msg)
	return &rpcpb.SendOutboundMessageResponse{Sent: sent}, nil
}

func (s *server) LoadSnapshot(ctx context.Context, req *rpcpb.LoadSnapshotRequest) (*rpcpb.LoadSnapshotResponse, error) {
	// if timeout is too small or not set, default to 5-min
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < defaultStartTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), defaultStartTimeout)
		_ = cancel // don't call since "start" is async, "curl" may not specify timeout
		zap.L().Info("received start request with default timeout", zap.String("timeout", defaultStartTimeout.String()))
	} else {
		zap.L().Info("received start request with existing timeout", zap.String("deadline", deadline.String()))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// If [clusterInfo] is already populated, the server has already been started.
	if s.clusterInfo != nil {
		return nil, ErrAlreadyBootstrapped
	}

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
		Healthy:     false,
	}

	zap.L().Info("starting",
		zap.Int32("pid", pid),
		zap.String("rootDataDir", rootDataDir),
	)

	if s.network != nil {
		return nil, ErrAlreadyBootstrapped
	}

	s.network, err = newLocalNetwork(localNetworkOptions{
		execPath:         req.GetExecPath(),
		pluginDir:        req.GetPluginDir(),
		rootDataDir:      rootDataDir,
		chainConfigs:     req.ChainConfigs,
		globalNodeConfig: req.GetGlobalNodeConfig(),

		// to block racey restart
		// "s.network.start" runs asynchronously
		// so it would not deadlock with the acquired lock
		// in this "Start" method
		restartMu: s.mu,

		snapshotsDir: s.cfg.SnapshotsDir,
	})
	if err != nil {
		return nil, err
	}

	// blocking load snapshot to soon get not found snapshot errors
	if err := s.network.loadSnapshot(ctx, req.SnapshotName); err != nil {
		zap.L().Warn("snapshot load failed to complete", zap.Error(err))
		s.network = nil
		s.clusterInfo = nil
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
		s.waitChAndUpdateClusterInfo("waiting for local cluster readiness", readyCh, true)
	}()

	return &rpcpb.LoadSnapshotResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) SaveSnapshot(ctx context.Context, req *rpcpb.SaveSnapshotRequest) (*rpcpb.SaveSnapshotResponse, error) {
	zap.L().Info("received save snapshot request", zap.String("snapshot-name", req.SnapshotName))
	info := s.getClusterInfo()
	if info == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	snapshotPath, err := s.network.nw.SaveSnapshot(ctx, req.SnapshotName)
	if err != nil {
		zap.L().Warn("snapshot save failed to complete", zap.Error(err))
		return nil, err
	}
	s.network = nil
	s.clusterInfo = nil

	return &rpcpb.SaveSnapshotResponse{SnapshotPath: snapshotPath}, nil
}

func (s *server) RemoveSnapshot(ctx context.Context, req *rpcpb.RemoveSnapshotRequest) (*rpcpb.RemoveSnapshotResponse, error) {
	zap.L().Info("received remove snapshot request", zap.String("snapshot-name", req.SnapshotName))
	info := s.getClusterInfo()
	if info == nil {
		return nil, ErrNotBootstrapped
	}

	if err := s.network.nw.RemoveSnapshot(req.SnapshotName); err != nil {
		zap.L().Warn("snapshot remove failed to complete", zap.Error(err))
		return nil, err
	}
	return &rpcpb.RemoveSnapshotResponse{}, nil
}

func (s *server) GetSnapshotNames(ctx context.Context, req *rpcpb.GetSnapshotNamesRequest) (*rpcpb.GetSnapshotNamesResponse, error) {
	zap.L().Info("get snapshot names")
	info := s.getClusterInfo()
	if info == nil {
		return nil, ErrNotBootstrapped
	}

	snapshotNames, err := s.network.nw.GetSnapshotNames()
	if err != nil {
		return nil, err
	}
	return &rpcpb.GetSnapshotNamesResponse{SnapshotNames: snapshotNames}, nil
}

func (s *server) getClusterInfo() *rpcpb.ClusterInfo {
	s.mu.RLock()
	info := s.clusterInfo
	s.mu.RUnlock()
	return info
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
