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
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/snow/networking/router"
	"github.com/ava-labs/avalanchego/staking"
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
	GwDisabled  bool
	DialTimeout time.Duration
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
	ErrUnexpectedType                     = errors.New("unexpected type")
	ErrStatusCanceled                     = errors.New("gRPC stream status canceled")
)

const (
	MinNodes            uint32 = 1
	DefaultNodes        uint32 = 5
	StopOnSignalTimeout        = 2 * time.Second
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
		stopCtx, stopCtxCancel := context.WithTimeout(context.Background(), StopOnSignalTimeout)
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

const DefaultStartTimeout = 5 * time.Minute

func (s *server) Start(ctx context.Context, req *rpcpb.StartRequest) (*rpcpb.StartResponse, error) {
	// if timeout is too small or not set, default to 5-min
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < DefaultStartTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), DefaultStartTimeout)
		_ = cancel // don't call since "start" is async, "curl" may not specify timeout
		zap.L().Info("received start request with default timeout", zap.String("timeout", DefaultStartTimeout.String()))
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
	customVMs := make(map[string][]byte)
	if req.GetPluginDir() == "" {
		if len(req.GetCustomVms()) > 0 {
			return nil, ErrPluginDirEmptyButCustomVMsNotEmpty
		}
		if err := utils.CheckExecPluginPaths(req.GetExecPath(), "", ""); err != nil {
			return nil, err
		}
	} else {
		if len(req.GetCustomVms()) == 0 {
			return nil, ErrPluginDirNonEmptyButCustomVMsEmpty
		}
		zap.L().Info("non-empty plugin dir", zap.String("plugin-dir", req.GetPluginDir()))
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
			if err := utils.CheckExecPluginPaths(
				req.GetExecPath(),
				filepath.Join(req.GetPluginDir(), vmID.String()),
				vmGenesisFilePath,
			); err != nil {
				return nil, err
			}
			b, err := ioutil.ReadFile(vmGenesisFilePath)
			if err != nil {
				return nil, err
			}
			customVMs[vmName] = b
		}
	}
	pluginDir := ""
	if req.GetPluginDir() != "" {
		pluginDir = req.GetPluginDir()
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
		nodeLogLevel       = req.GetNodeLogLevel()
		globalNodeConfig   = req.GetGlobalNodeConfig()
		customNodeConfigs  = req.GetCustomNodeConfigs()
		err                error
	)
	if len(rootDataDir) == 0 {
		rootDataDir, err = ioutil.TempDir(os.TempDir(), "network-runner-root-data")
		if err != nil {
			return nil, err
		}
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
		zap.String("defaultNodeConfig", globalNodeConfig),
	)

	if s.network != nil {
		return nil, ErrAlreadyBootstrapped
	}

	if len(customNodeConfigs) > 0 {
		zap.L().Warn("custom node configs have been provided; ignoring the 'number-of-nodes' parameter and setting it to", zap.Int("numNodes", len(customNodeConfigs)))
		numNodes = uint32(len(customNodeConfigs))
	}

	s.network, err = newLocalNetwork(localNetworkOptions{
		execPath:           execPath,
		rootDataDir:        rootDataDir,
		numNodes:           numNodes,
		whitelistedSubnets: whitelistedSubnets,
		nodeLogLevel:       nodeLogLevel,
		pluginDir:          pluginDir,
		customVMs:          customVMs,
		globalNodeConfig:   globalNodeConfig,
		customNodeConfigs:  customNodeConfigs,

		// to block racey restart
		// "s.network.start" runs asynchronously
		// so it would not deadlock with the acquired lock
		// in this "Start" method
		restartMu: s.mu,
	})
	if err != nil {
		return nil, err
	}

	// start non-blocking to install local cluster + custom VMs (if applicable)
	// the user is expected to poll cluster status
	go s.network.start(ctx)

	// update cluster info non-blocking
	// the user is expected to poll this latest information
	// to decide cluster/subnet readiness
	go func() {
		zap.L().Info("waiting for local cluster readiness")
		select {
		case <-s.closed:
			return
		case <-s.network.stopc:
			// TODO: fix race from shutdown
			return
		case serr := <-s.network.startErrc:
			zap.L().Warn("start failed to complete", zap.Error(serr))
			panic(serr)
		case <-s.network.localClusterReadyc:
			s.mu.Lock()
			s.clusterInfo.NodeNames = s.network.nodeNames
			s.clusterInfo.NodeInfos = s.network.nodeInfos
			s.clusterInfo.Healthy = true
			s.mu.Unlock()
		}

		if len(req.GetCustomVms()) == 0 {
			zap.L().Info("no custom VM installation request, skipping its readiness check")
		} else {
			zap.L().Info("waiting for custom VMs readiness")
			select {
			case <-s.closed:
				return
			case <-s.network.stopc:
				return
			case serr := <-s.network.startErrc:
				zap.L().Warn("start custom VMs failed to complete", zap.Error(serr))
				panic(serr)
			case <-s.network.customVMsReadyc:
				s.mu.Lock()
				s.clusterInfo.CustomVmsHealthy = true
				s.clusterInfo.CustomVms = make(map[string]*rpcpb.CustomVmInfo)
				for vmID, vmInfo := range s.network.customVMIDToInfo {
					s.clusterInfo.CustomVms[vmID.String()] = vmInfo.info
				}
				s.mu.Unlock()
			}
		}
	}()

	return &rpcpb.StartResponse{ClusterInfo: s.clusterInfo}, nil
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

	s.network.nodeNames = make([]string, 0)
	for name := range s.network.nodeInfos {
		s.network.nodeNames = append(s.network.nodeNames, name)
	}
	s.clusterInfo.NodeNames = s.network.nodeNames
	s.clusterInfo.NodeInfos = s.network.nodeInfos

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

	var nodeLogLevel, whitelistedSubnets, pluginDir string

	if _, exists := s.network.nodeInfos[req.Name]; exists {
		return nil, fmt.Errorf("node with name %s already exists", req.Name)
	}
	// user can override bin path for this node...
	execPath := req.StartRequest.ExecPath
	if execPath == "" {
		// ...or use the same binary as the rest of the network
		execPath = s.network.binPath
	}
	_, err := os.Stat(execPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, utils.ErrNotExists
		}
		return nil, fmt.Errorf("failed to stat exec %q (%w)", execPath, err)
	}
	if req.StartRequest.NodeLogLevel != nil {
		nodeLogLevel = *req.StartRequest.NodeLogLevel
	}

	// use same configs from other nodes
	whitelistedSubnets = s.network.options.whitelistedSubnets
	pluginDir = s.network.options.pluginDir

	rootDataDir := s.clusterInfo.RootDataDir

	logDir := filepath.Join(rootDataDir, req.Name, "log")
	dbDir := filepath.Join(rootDataDir, req.Name, "db-dir")

	var defaultConfig, globalConfig map[string]interface{}
	if err := json.Unmarshal([]byte(defaultNodeConfig), &defaultConfig); err != nil {
		return nil, err
	}
	if req.StartRequest.GetGlobalNodeConfig() != "" {
		if err := json.Unmarshal([]byte(req.StartRequest.GetGlobalNodeConfig()), &globalConfig); err != nil {
			return nil, err
		}
	}

	var mergedConfig map[string]interface{}
	// we only need to merge from the default node config here, as we are only adding one node
	mergedConfig, err = mergeNodeConfig(defaultConfig, globalConfig, "")
	if err != nil {
		return nil, fmt.Errorf("failed merging provided configs: %w", err)
	}
	configFile, err := createConfigFileString(mergedConfig, nodeLogLevel, logDir, dbDir, pluginDir, whitelistedSubnets)
	if err != nil {
		return nil, fmt.Errorf("failed to generate json node config string: %w", err)
	}
	stakingCert, stakingKey, err := staking.NewCertAndKeyBytes()
	if err != nil {
		return nil, fmt.Errorf("couldn't generate staking Cert/Key: %w", err)
	}

	nodeConfig := node.Config{
		Name:           req.Name,
		ConfigFile:     configFile,
		StakingKey:     string(stakingKey),
		StakingCert:    string(stakingCert),
		BinaryPath:     execPath,
		RedirectStdout: true,
		RedirectStderr: true,
	}
	_, err = s.network.nw.AddNode(nodeConfig)
	if err != nil {
		return nil, err
	}

	s.network.nodeNames = append(s.network.nodeNames, req.Name)

	info := &rpcpb.NodeInfo{
		Name:               req.Name,
		ExecPath:           execPath,
		Uri:                "",
		Id:                 "",
		LogDir:             logDir,
		DbDir:              dbDir,
		WhitelistedSubnets: whitelistedSubnets,
		Config:             []byte(configFile),
	}
	s.network.nodeInfos[req.Name] = info

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

	if err := s.network.nw.RemoveNode(req.Name); err != nil {
		return nil, err
	}
	delete(s.network.nodeInfos, req.Name)
	s.network.nodeNames = make([]string, 0)
	for name := range s.network.nodeInfos {
		s.network.nodeNames = append(s.network.nodeNames, name)
	}
	s.clusterInfo.NodeNames = s.network.nodeNames
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	zap.L().Info("waiting for local cluster readiness")
	if err := s.network.waitForLocalClusterReady(ctx); err != nil {
		return nil, err
	}

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

	found, idx := false, 0
	oldNodeConfig := node.Config{}
	for i, cfg := range s.network.cfg.NodeConfigs {
		if cfg.Name == req.Name {
			oldNodeConfig = cfg
			found = true
			idx = i
			break
		}
	}
	if !found {
		return nil, ErrNodeNotFound
	}
	nodeConfig := oldNodeConfig

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
	nodeLogLevel := "INFO"
	if req.GetNodeLogLevel() != "" {
		nodeLogLevel = strings.ToUpper(req.GetNodeLogLevel())
	}

	nodeConfig.ConfigFile = fmt.Sprintf(`{
	"network-peer-list-gossip-frequency":"250ms",
	"network-max-reconnect-delay":"1s",
	"public-ip":"127.0.0.1",
	"health-check-frequency":"2s",
	"api-admin-enabled":true,
	"api-ipcs-enabled":true,
	"index-enabled":true,
	"log-display-level":"%s",
	"log-level":"%s",
	"log-dir":"%s",
	"db-dir":"%s",
	"plugin-dir":"%s",
	"whitelisted-subnets":"%s"
}`,
		nodeLogLevel,
		nodeLogLevel,
		nodeInfo.LogDir,
		nodeInfo.DbDir,
		nodeInfo.PluginDir,
		nodeInfo.WhitelistedSubnets,
	)
	nodeConfig.BinaryPath = nodeInfo.ExecPath
	nodeConfig.RedirectStdout = true
	nodeConfig.RedirectStderr = true

	// now remove the node before restart
	zap.L().Info("removing the node")
	if err := s.network.nw.RemoveNode(req.Name); err != nil {
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
	s.network.cfg.NodeConfigs[idx] = nodeConfig
	s.clusterInfo.NodeInfos = s.network.nodeInfos

	return &rpcpb.RestartNodeResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) Stop(ctx context.Context, req *rpcpb.StopRequest) (*rpcpb.StopResponse, error) {
	zap.L().Debug("received stop request")
	info := s.getClusterInfo()
	if info == nil {
		return nil, ErrNotBootstrapped
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.network.stop(ctx)
	s.network = nil
	info.Healthy = false
	s.clusterInfo = nil

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
	sent := attachedPeer.Send(msg)
	return &rpcpb.SendOutboundMessageResponse{Sent: sent}, nil
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
