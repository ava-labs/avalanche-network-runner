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
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type Config struct {
	Port        string
	GwPort      string
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

	mu          sync.RWMutex
	clusterInfo *rpcpb.ClusterInfo
	network     *localNetwork

	rpcpb.UnimplementedPingServiceServer
	rpcpb.UnimplementedControlServiceServer
}

var (
	ErrNotExists   = errors.New("not exists")
	ErrInvalidPort = errors.New("invalid port")
	ErrClosed      = errors.New("server closed")
)

func New(cfg Config) (Server, error) {
	if cfg.Port == "" || cfg.GwPort == "" {
		return nil, ErrInvalidPort
	}

	ln, err := net.Listen("tcp", cfg.Port)
	if err != nil {
		return nil, err
	}
	gwMux := runtime.NewServeMux()
	return &server{
		cfg: cfg,

		closed: make(chan struct{}),

		ln:         ln,
		gRPCServer: grpc.NewServer(),

		gwMux: gwMux,
		gwServer: &http.Server{
			Addr:    cfg.GwPort,
			Handler: gwMux,
		},
	}, nil
}

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
	go func() {
		zap.L().Info("dialing gRPC server", zap.String("port", s.cfg.Port))
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

	select {
	case <-rootCtx.Done():
		zap.L().Warn("root context is done")

		zap.L().Warn("closed gRPC gateway server", zap.Error(s.gwServer.Close()))
		<-gwErrc

		s.gRPCServer.Stop()
		zap.L().Warn("closed gRPC server")
		<-gRPCErrc

	case err = <-gRPCErrc:
		zap.L().Warn("gRPC server failed", zap.Error(err))
		zap.L().Warn("closed gRPC gateway server", zap.Error(s.gwServer.Close()))
		<-gwErrc

	case err = <-gwErrc:
		zap.L().Warn("gRPC gateway server failed", zap.Error(err))
		s.gRPCServer.Stop()
		zap.L().Warn("closed gRPC server")
		<-gRPCErrc
	}

	s.closeOnce.Do(func() {
		close(s.closed)
	})
	return err
}

var (
	ErrAlreadyBootstrapped = errors.New("already bootstrapped")
	ErrNotBootstrapped     = errors.New("not bootstrapped")
	ErrNodeNotFound        = errors.New("node not found")
	ErrUnexpectedType      = errors.New("unexpected type")
	ErrStatusCanceled      = errors.New("gRPC stream status canceled")
)

func (s *server) Ping(ctx context.Context, req *rpcpb.PingRequest) (*rpcpb.PingResponse, error) {
	zap.L().Debug("received ping request")
	return &rpcpb.PingResponse{Pid: int32(os.Getpid())}, nil
}

func (s *server) Start(ctx context.Context, req *rpcpb.StartRequest) (*rpcpb.StartResponse, error) {
	zap.L().Info("received start request")

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
		logLevel           = req.GetLogLevel()
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
	)
	if _, err := os.Stat(req.ExecPath); err != nil {
		return nil, ErrNotExists
	}

	if s.network != nil {
		return nil, ErrAlreadyBootstrapped
	}

	s.network, err = newNetwork(execPath, rootDataDir, numNodes, whitelistedSubnets, logLevel)
	if err != nil {
		return nil, err
	}
	go s.network.start()

	go func() {
		select {
		case <-s.closed:
			return
		case <-s.network.stopc:
			// TODO: fix race from shutdown
			return
		case <-s.network.readyc:
			s.mu.Lock()
			s.clusterInfo.NodeNames = s.network.nodeNames
			s.clusterInfo.NodeInfos = s.network.nodeInfos
			s.clusterInfo.Healthy = true
			s.mu.Unlock()
		}
	}()
	return &rpcpb.StartResponse{ClusterInfo: s.clusterInfo}, nil
}

func (s *server) Health(ctx context.Context, req *rpcpb.HealthRequest) (*rpcpb.HealthResponse, error) {
	zap.L().Debug("health")
	if info := s.getClusterInfo(); info == nil {
		return nil, ErrNotBootstrapped
	}

	zap.L().Info("waiting for healthy")
	if err := s.network.waitForHealthy(); err != nil {
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

	zap.L().Info("waiting for healthy")
	if err := s.network.waitForHealthy(); err != nil {
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

	// keep everything same except config file and binary path
	nodeInfo.ExecPath = req.StartRequest.ExecPath
	nodeInfo.WhitelistedSubnets = *req.StartRequest.WhitelistedSubnets
	nodeConfig.ConfigFile = fmt.Sprintf(`{
	"network-peer-list-gossip-frequency":"250ms",
	"network-max-reconnect-delay":"1s",
	"public-ip":"127.0.0.1",
	"health-check-frequency":"2s",
	"api-admin-enabled":true,
	"api-ipcs-enabled":true,
	"index-enabled":true,
	"log-display-level":"INFO",
	"log-level":"INFO",
	"log-dir":"%s",
	"db-dir":"%s",
	"whitelisted-subnets":"%s"
}`,
		nodeInfo.LogDir,
		nodeInfo.DbDir,
		nodeInfo.WhitelistedSubnets,
	)
	nodeConfig.ImplSpecificConfig = json.RawMessage(fmt.Sprintf(`{"binaryPath":"%s","redirectStdout":true,"redirectStderr":true}`, nodeInfo.ExecPath))

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

	zap.L().Info("waiting for healthy")
	if err := s.network.waitForHealthy(); err != nil {
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

	s.network.stop()
	s.network = nil
	info.Healthy = false
	s.clusterInfo = nil

	return &rpcpb.StopResponse{ClusterInfo: info}, nil
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
