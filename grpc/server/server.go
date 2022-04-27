// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner/backend"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var ErrInvalidPort = errors.New("invalid port")

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

	PingServiceHandler
	OrchestratorServiceHandler
}

func New(cfg Config, orchestrator backend.NetworkOrchestrator) (Server, error) {
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
		OrchestratorServiceHandler: *NewOrchestatorServiceHandler(orchestrator),
	}, nil
}

func (s *server) Run(rootCtx context.Context) (err error) {
	s.rootCtx = rootCtx
	s.registerServiceServers()

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

		if err := s.registerServiceHandlers(rootCtx, gwConn); err != nil {
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

func (s *server) registerServiceServers() {
	s.gRPCRegisterOnce.Do(func() {
		rpcpb.RegisterPingServiceServer(s.gRPCServer, s)
		rpcpb.RegisterOrchestratorServiceServer(s.gRPCServer, s)
	})
}

func (s *server) registerServiceHandlers(ctx context.Context, gwConn *grpc.ClientConn) error {
	if err := rpcpb.RegisterPingServiceHandler(ctx, s.gwMux, gwConn); err != nil {
		return err
	}
	if err := rpcpb.RegisterOrchestratorServiceHandler(ctx, s.gwMux, gwConn); err != nil {
		return err
	}

	return nil
}
