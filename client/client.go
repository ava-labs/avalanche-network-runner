// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// Package client implements client.
package client

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type Config struct {
	Endpoint    string
	DialTimeout time.Duration
}

type Client interface {
	Ping(ctx context.Context) (*rpcpb.PingResponse, error)
	RPCVersion(ctx context.Context) (*rpcpb.RPCVersionResponse, error)
	Start(ctx context.Context, execPath string, opts ...OpOption) (*rpcpb.StartResponse, error)
	CreateBlockchains(ctx context.Context, blockchainSpecs []*rpcpb.BlockchainSpec) (*rpcpb.CreateBlockchainsResponse, error)
	CreateSubnets(ctx context.Context, subnetSpecs []*rpcpb.SubnetSpec) (*rpcpb.CreateSubnetsResponse, error)
	TransformElasticSubnets(ctx context.Context, elasticSubnetSpecs []*rpcpb.ElasticSubnetSpec) (*rpcpb.TransformElasticSubnetsResponse, error)
	AddPermissionlessValidator(ctx context.Context, validatorSpec []*rpcpb.PermissionlessStakerSpec) (*rpcpb.AddPermissionlessValidatorResponse, error)
	AddPermissionlessDelegator(ctx context.Context, validatorSpec []*rpcpb.PermissionlessStakerSpec) (*rpcpb.AddPermissionlessDelegatorResponse, error)
	RemoveSubnetValidator(ctx context.Context, validatorSpec []*rpcpb.RemoveSubnetValidatorSpec) (*rpcpb.RemoveSubnetValidatorResponse, error)
	AddSubnetValidators(ctx context.Context, validatorSpec []*rpcpb.SubnetValidatorsSpec) (*rpcpb.AddSubnetValidatorsResponse, error)
	Health(ctx context.Context) (*rpcpb.HealthResponse, error)
	WaitForHealthy(ctx context.Context) (*rpcpb.WaitForHealthyResponse, error)
	URIs(ctx context.Context) ([]string, error)
	Status(ctx context.Context) (*rpcpb.StatusResponse, error)
	StreamStatus(ctx context.Context, pushInterval time.Duration) (<-chan *rpcpb.ClusterInfo, error)
	RemoveNode(ctx context.Context, name string) (*rpcpb.RemoveNodeResponse, error)
	PauseNode(ctx context.Context, name string) (*rpcpb.PauseNodeResponse, error)
	ResumeNode(ctx context.Context, name string) (*rpcpb.ResumeNodeResponse, error)
	RestartNode(ctx context.Context, name string, opts ...OpOption) (*rpcpb.RestartNodeResponse, error)
	AddNode(ctx context.Context, name string, execPath string, opts ...OpOption) (*rpcpb.AddNodeResponse, error)
	Stop(ctx context.Context) (*rpcpb.StopResponse, error)
	AttachPeer(ctx context.Context, nodeName string) (*rpcpb.AttachPeerResponse, error)
	SendOutboundMessage(ctx context.Context, nodeName string, peerID string, op uint32, msgBody []byte) (*rpcpb.SendOutboundMessageResponse, error)
	Close() error
	SaveSnapshot(ctx context.Context, snapshotName string, force bool) (*rpcpb.SaveSnapshotResponse, error)
	LoadSnapshot(ctx context.Context, snapshotName string, inPlace bool, opts ...OpOption) (*rpcpb.LoadSnapshotResponse, error)
	RemoveSnapshot(ctx context.Context, snapshotName string) (*rpcpb.RemoveSnapshotResponse, error)
	ListSnapshots(ctx context.Context) ([]string, error)
	ListSubnets(ctx context.Context) ([]string, error)
	ListBlockchains(ctx context.Context) ([]*rpcpb.CustomChainInfo, error)
	ListRpcs(ctx context.Context) ([]*rpcpb.BlockchainRpcs, error)
	VMID(ctx context.Context, vmName string) (string, error)
}

type client struct {
	cfg Config
	log logging.Logger

	conn *grpc.ClientConn

	pingc    rpcpb.PingServiceClient
	controlc rpcpb.ControlServiceClient

	closed    chan struct{}
	closeOnce sync.Once
}

func New(cfg Config, log logging.Logger) (Client, error) {
	log.Debug("dialing server at ", zap.String("endpoint", cfg.Endpoint))

	ctx, cancel := context.WithTimeout(context.Background(), cfg.DialTimeout)
	conn, err := grpc.DialContext(
		ctx,
		cfg.Endpoint,
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	cancel()
	if err != nil {
		return nil, err
	}

	return &client{
		cfg:      cfg,
		log:      log,
		conn:     conn,
		pingc:    rpcpb.NewPingServiceClient(conn),
		controlc: rpcpb.NewControlServiceClient(conn),
		closed:   make(chan struct{}),
	}, nil
}

func (c *client) Ping(ctx context.Context) (*rpcpb.PingResponse, error) {
	c.log.Info("ping")

	// ref. https://grpc-ecosystem.github.io/grpc-gateway/docs/tutorials/adding_annotations/
	// curl -X POST -k http://localhost:8081/v1/ping -d ''
	return c.pingc.Ping(ctx, &rpcpb.PingRequest{})
}

func (c *client) RPCVersion(ctx context.Context) (*rpcpb.RPCVersionResponse, error) {
	c.log.Info("rpc version")
	return c.controlc.RPCVersion(ctx, &rpcpb.RPCVersionRequest{})
}

func (c *client) Start(ctx context.Context, execPath string, opts ...OpOption) (*rpcpb.StartResponse, error) {
	ret := &Op{numNodes: constants.DefaultNumNodes}
	ret.applyOpts(opts)

	req := &rpcpb.StartRequest{
		NetworkId:      ret.networkID,
		ExecPath:       execPath,
		NumNodes:       &ret.numNodes,
		ChainConfigs:   ret.chainConfigs,
		UpgradeConfigs: ret.upgradeConfigs,
		SubnetConfigs:  ret.subnetConfigs,
	}
	if ret.trackSubnets != "" {
		req.WhitelistedSubnets = &ret.trackSubnets
	}
	if ret.rootDataDir != "" {
		req.RootDataDir = &ret.rootDataDir
	}
	if ret.logRootDir != "" {
		req.LogRootDir = &ret.logRootDir
	}
	if ret.pluginDir != "" {
		req.PluginDir = ret.pluginDir
	}
	if len(ret.blockchainSpecs) > 0 {
		req.BlockchainSpecs = ret.blockchainSpecs
	}
	if ret.globalNodeConfig != "" {
		req.GlobalNodeConfig = &ret.globalNodeConfig
	}
	if ret.customNodeConfigs != nil {
		req.CustomNodeConfigs = ret.customNodeConfigs
	}
	if ret.walletPrivateKey != "" {
		req.WalletPrivateKey = ret.walletPrivateKey
	}
	if ret.customNetworkGenesisPath != "" {
		req.CustomNetworkGenesisPath = ret.customNetworkGenesisPath
	}
	if len(ret.customNetworkBootstrapNodeIPPortPairs) > 0 {
		req.CustomNetworkBootstrapIpPortPairs = ret.customNetworkBootstrapNodeIPPortPairs
	}
	if len(ret.customNetworkBootstrapNodeIDs) > 0 {
		req.CustomNetworkBootstrapNodeIds = ret.customNetworkBootstrapNodeIDs
	}
	if ret.upgradePath != "" {
		req.UpgradePath = ret.upgradePath
	}
	req.ReassignPortsIfUsed = &ret.reassignPortsIfUsed
	req.DynamicPorts = &ret.dynamicPorts

	c.log.Info("start")
	return c.controlc.Start(ctx, req)
}

func (c *client) CreateBlockchains(ctx context.Context, blockchainSpecs []*rpcpb.BlockchainSpec) (*rpcpb.CreateBlockchainsResponse, error) {
	req := &rpcpb.CreateBlockchainsRequest{
		BlockchainSpecs: blockchainSpecs,
	}

	c.log.Info("create blockchains")
	return c.controlc.CreateBlockchains(ctx, req)
}

func (c *client) CreateSubnets(ctx context.Context, subnetSpecs []*rpcpb.SubnetSpec) (*rpcpb.CreateSubnetsResponse, error) {
	req := &rpcpb.CreateSubnetsRequest{
		SubnetSpecs: subnetSpecs,
	}

	c.log.Info("create subnets")
	return c.controlc.CreateSubnets(ctx, req)
}

func (c *client) TransformElasticSubnets(ctx context.Context, elasticSubnetSpecs []*rpcpb.ElasticSubnetSpec) (*rpcpb.TransformElasticSubnetsResponse, error) {
	req := &rpcpb.TransformElasticSubnetsRequest{
		ElasticSubnetSpec: elasticSubnetSpecs,
	}

	c.log.Info("transform subnets")
	return c.controlc.TransformElasticSubnets(ctx, req)
}

func (c *client) AddPermissionlessDelegator(ctx context.Context, validatorSpec []*rpcpb.PermissionlessStakerSpec) (*rpcpb.AddPermissionlessDelegatorResponse, error) {
	req := &rpcpb.AddPermissionlessDelegatorRequest{
		ValidatorSpec: validatorSpec,
	}

	c.log.Info("add permissionless delegators to elastic subnets")
	return c.controlc.AddPermissionlessDelegator(ctx, req)
}

func (c *client) AddPermissionlessValidator(ctx context.Context, validatorSpec []*rpcpb.PermissionlessStakerSpec) (*rpcpb.AddPermissionlessValidatorResponse, error) {
	req := &rpcpb.AddPermissionlessValidatorRequest{
		ValidatorSpec: validatorSpec,
	}

	c.log.Info("add permissionless validators to elastic subnets")
	return c.controlc.AddPermissionlessValidator(ctx, req)
}

func (c *client) RemoveSubnetValidator(ctx context.Context, validatorSpec []*rpcpb.RemoveSubnetValidatorSpec) (*rpcpb.RemoveSubnetValidatorResponse, error) {
	req := &rpcpb.RemoveSubnetValidatorRequest{
		ValidatorSpec: validatorSpec,
	}

	c.log.Info("remove subnet validator")
	return c.controlc.RemoveSubnetValidator(ctx, req)
}

func (c *client) AddSubnetValidators(ctx context.Context, validatorSpec []*rpcpb.SubnetValidatorsSpec) (*rpcpb.AddSubnetValidatorsResponse, error) {
	req := &rpcpb.AddSubnetValidatorsRequest{
		ValidatorsSpec: validatorSpec,
	}

	c.log.Info("add subnet validators")
	return c.controlc.AddSubnetValidators(ctx, req)
}

func (c *client) Health(ctx context.Context) (*rpcpb.HealthResponse, error) {
	c.log.Info("health")
	return c.controlc.Health(ctx, &rpcpb.HealthRequest{})
}

func (c *client) WaitForHealthy(ctx context.Context) (*rpcpb.WaitForHealthyResponse, error) {
	c.log.Info("wait for healthy")
	return c.controlc.WaitForHealthy(ctx, &rpcpb.WaitForHealthyRequest{})
}

func (c *client) URIs(ctx context.Context) ([]string, error) {
	c.log.Info("uris")
	resp, err := c.controlc.URIs(ctx, &rpcpb.URIsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Uris, nil
}

func (c *client) Status(ctx context.Context) (*rpcpb.StatusResponse, error) {
	c.log.Info("status")
	return c.controlc.Status(ctx, &rpcpb.StatusRequest{})
}

func (c *client) StreamStatus(ctx context.Context, pushInterval time.Duration) (<-chan *rpcpb.ClusterInfo, error) {
	stream, err := c.controlc.StreamStatus(ctx, &rpcpb.StreamStatusRequest{
		PushInterval: int64(pushInterval),
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan *rpcpb.ClusterInfo, 1)
	go func() {
		defer func() {
			c.log.Debug("closing stream send", zap.Error(stream.CloseSend()))
			close(ch)
		}()
		c.log.Info("start receive routine")
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.closed:
				return
			default:
			}

			// receive data from stream
			msg := new(rpcpb.StatusResponse)
			err := stream.RecvMsg(msg)
			if err == nil {
				ch <- msg.GetClusterInfo()
				continue
			}

			if errors.Is(err, io.EOF) {
				c.log.Debug("received EOF from client; returning to close the stream from server side")
				return
			}
			if isClientCanceled(stream.Context().Err(), err) {
				c.log.Warn("failed to receive status request from gRPC stream due to client cancellation", zap.Error(err))
			} else {
				c.log.Warn("failed to receive status request from gRPC stream", zap.Error(err))
			}
			return
		}
	}()
	return ch, nil
}

func (c *client) Stop(ctx context.Context) (*rpcpb.StopResponse, error) {
	c.log.Info("stop")
	return c.controlc.Stop(ctx, &rpcpb.StopRequest{})
}

func (c *client) AddNode(ctx context.Context, name string, execPath string, opts ...OpOption) (*rpcpb.AddNodeResponse, error) {
	ret := &Op{}
	ret.applyOpts(opts)

	req := &rpcpb.AddNodeRequest{
		Name:           name,
		ExecPath:       execPath,
		NodeConfig:     &ret.globalNodeConfig,
		ChainConfigs:   ret.chainConfigs,
		UpgradeConfigs: ret.upgradeConfigs,
		SubnetConfigs:  ret.subnetConfigs,
	}

	if ret.pluginDir != "" {
		req.PluginDir = ret.pluginDir
	}

	c.log.Info("add node", zap.String("name", name))
	return c.controlc.AddNode(ctx, req)
}

func (c *client) RemoveNode(ctx context.Context, name string) (*rpcpb.RemoveNodeResponse, error) {
	c.log.Info("remove node", zap.String("name", name))
	return c.controlc.RemoveNode(ctx, &rpcpb.RemoveNodeRequest{Name: name})
}

func (c *client) PauseNode(ctx context.Context, name string) (*rpcpb.PauseNodeResponse, error) {
	c.log.Info("pause node", zap.String("name", name))
	return c.controlc.PauseNode(ctx, &rpcpb.PauseNodeRequest{Name: name})
}

func (c *client) ResumeNode(ctx context.Context, name string) (*rpcpb.ResumeNodeResponse, error) {
	c.log.Info("resume node", zap.String("name", name))
	return c.controlc.ResumeNode(ctx, &rpcpb.ResumeNodeRequest{Name: name})
}

func (c *client) RestartNode(ctx context.Context, name string, opts ...OpOption) (*rpcpb.RestartNodeResponse, error) {
	ret := &Op{}
	ret.applyOpts(opts)

	req := &rpcpb.RestartNodeRequest{Name: name}
	if ret.execPath != "" {
		req.ExecPath = &ret.execPath
	}
	if ret.pluginDir != "" {
		req.PluginDir = ret.pluginDir
	}
	if ret.trackSubnets != "" {
		req.WhitelistedSubnets = &ret.trackSubnets
	}
	req.ChainConfigs = ret.chainConfigs
	req.UpgradeConfigs = ret.upgradeConfigs
	req.SubnetConfigs = ret.subnetConfigs

	c.log.Info("restart node", zap.String("name", name))
	return c.controlc.RestartNode(ctx, req)
}

func (c *client) AttachPeer(ctx context.Context, nodeName string) (*rpcpb.AttachPeerResponse, error) {
	c.log.Info("attaching peer", zap.String("name", nodeName))
	return c.controlc.AttachPeer(ctx, &rpcpb.AttachPeerRequest{NodeName: nodeName})
}

func (c *client) SendOutboundMessage(ctx context.Context, nodeName string, peerID string, op uint32, msgBody []byte) (*rpcpb.SendOutboundMessageResponse, error) {
	c.log.Info("sending outbound message", zap.String("name", nodeName), zap.String("peer-ID", peerID))
	return c.controlc.SendOutboundMessage(ctx, &rpcpb.SendOutboundMessageRequest{
		NodeName: nodeName,
		PeerId:   peerID,
		Op:       op,
		Bytes:    msgBody,
	})
}

func (c *client) SaveSnapshot(ctx context.Context, snapshotName string, force bool) (*rpcpb.SaveSnapshotResponse, error) {
	c.log.Info("save snapshot", zap.String("snapshot-name", snapshotName))
	return c.controlc.SaveSnapshot(ctx, &rpcpb.SaveSnapshotRequest{SnapshotName: snapshotName, Force: force})
}

func (c *client) LoadSnapshot(ctx context.Context, snapshotName string, inPlace bool, opts ...OpOption) (*rpcpb.LoadSnapshotResponse, error) {
	c.log.Info("load snapshot", zap.String("snapshot-name", snapshotName))
	ret := &Op{}
	ret.applyOpts(opts)
	req := rpcpb.LoadSnapshotRequest{
		SnapshotName:   snapshotName,
		ChainConfigs:   ret.chainConfigs,
		UpgradeConfigs: ret.upgradeConfigs,
		SubnetConfigs:  ret.subnetConfigs,
		InPlace:        inPlace,
	}
	if ret.execPath != "" {
		req.ExecPath = &ret.execPath
	}
	if ret.pluginDir != "" {
		req.PluginDir = ret.pluginDir
	}
	if ret.rootDataDir != "" {
		req.RootDataDir = &ret.rootDataDir
	}
	if ret.logRootDir != "" {
		req.LogRootDir = &ret.logRootDir
	}
	if ret.globalNodeConfig != "" {
		req.GlobalNodeConfig = &ret.globalNodeConfig
	}
	if ret.walletPrivateKey != "" {
		req.WalletPrivateKey = ret.walletPrivateKey
	}
	if ret.customNetworkGenesisPath != "" {
		req.CustomNetworkGenesisPath = ret.customNetworkGenesisPath
	}
	if len(ret.customNetworkBootstrapNodeIPPortPairs) > 0 {
		req.CustomNetworkBootstrapIpPortPairs = ret.customNetworkBootstrapNodeIPPortPairs
	}
	if len(ret.customNetworkBootstrapNodeIDs) > 0 {
		req.CustomNetworkBootstrapNodeIds = ret.customNetworkBootstrapNodeIDs
	}

	req.ReassignPortsIfUsed = &ret.reassignPortsIfUsed
	return c.controlc.LoadSnapshot(ctx, &req)
}

func (c *client) RemoveSnapshot(ctx context.Context, snapshotName string) (*rpcpb.RemoveSnapshotResponse, error) {
	c.log.Info("remove snapshot", zap.String("snapshot-name", snapshotName))
	return c.controlc.RemoveSnapshot(ctx, &rpcpb.RemoveSnapshotRequest{SnapshotName: snapshotName})
}

func (c *client) ListSnapshots(ctx context.Context) ([]string, error) {
	c.log.Info("list snapshots")
	resp, err := c.controlc.GetSnapshotNames(ctx, &rpcpb.GetSnapshotNamesRequest{})
	if err != nil {
		return nil, err
	}
	return resp.SnapshotNames, nil
}

func (c *client) ListSubnets(ctx context.Context) ([]string, error) {
	c.log.Info("list subnets")
	resp, err := c.controlc.ListSubnets(ctx, &rpcpb.ListSubnetsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.SubnetIds, nil
}

func (c *client) ListBlockchains(ctx context.Context) ([]*rpcpb.CustomChainInfo, error) {
	c.log.Info("list blockchains")
	resp, err := c.controlc.ListBlockchains(ctx, &rpcpb.ListBlockchainsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Blockchains, nil
}

func (c *client) ListRpcs(ctx context.Context) ([]*rpcpb.BlockchainRpcs, error) {
	c.log.Info("list rpcs")
	resp, err := c.controlc.ListRpcs(ctx, &rpcpb.ListRpcsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.BlockchainsRpcs, nil
}

func (c *client) VMID(ctx context.Context, vmName string) (string, error) {
	c.log.Info("vmid")
	resp, err := c.controlc.VMID(ctx, &rpcpb.VMIDRequest{VmName: vmName})
	if err != nil {
		return "", err
	}
	return resp.VmId, nil
}

func (c *client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
	return c.conn.Close()
}

type Op struct {
	numNodes                              uint32
	execPath                              string
	trackSubnets                          string
	globalNodeConfig                      string
	rootDataDir                           string
	logRootDir                            string
	pluginDir                             string
	blockchainSpecs                       []*rpcpb.BlockchainSpec
	customNodeConfigs                     map[string]string
	numSubnets                            uint32
	chainConfigs                          map[string]string
	upgradeConfigs                        map[string]string
	subnetConfigs                         map[string]string
	reassignPortsIfUsed                   bool
	dynamicPorts                          bool
	networkID                             uint32
	walletPrivateKey                      string
	customNetworkGenesisPath              string
	customNetworkBootstrapNodeIDs         []string
	customNetworkBootstrapNodeIPPortPairs []string
	upgradePath                           string
}

type OpOption func(*Op)

func (op *Op) applyOpts(opts []OpOption) {
	for _, opt := range opts {
		opt(op)
	}
}

func WithGlobalNodeConfig(nodeConfig string) OpOption {
	return func(op *Op) {
		op.globalNodeConfig = nodeConfig
	}
}

func WithNetworkID(networkID uint32) OpOption {
	return func(op *Op) {
		op.networkID = networkID
	}
}

func WithNumNodes(numNodes uint32) OpOption {
	return func(op *Op) {
		op.numNodes = numNodes
	}
}

func WithExecPath(execPath string) OpOption {
	return func(op *Op) {
		op.execPath = execPath
	}
}

func WithWhitelistedSubnets(trackSubnets string) OpOption {
	return func(op *Op) {
		op.trackSubnets = trackSubnets
	}
}

func WithTrackSubnets(trackSubnets string) OpOption {
	return func(op *Op) {
		op.trackSubnets = trackSubnets
	}
}

func WithRootDataDir(rootDataDir string) OpOption {
	return func(op *Op) {
		op.rootDataDir = rootDataDir
	}
}

func WithLogRootDir(logRootDir string) OpOption {
	return func(op *Op) {
		op.logRootDir = logRootDir
	}
}

func WithPluginDir(pluginDir string) OpOption {
	return func(op *Op) {
		op.pluginDir = pluginDir
	}
}

// Slice of BlockchainSpec
func WithBlockchainSpecs(blockchainSpecs []*rpcpb.BlockchainSpec) OpOption {
	return func(op *Op) {
		op.blockchainSpecs = blockchainSpecs
	}
}

// Map from chain name to its configuration json contents.
func WithChainConfigs(chainConfigs map[string]string) OpOption {
	return func(op *Op) {
		op.chainConfigs = chainConfigs
	}
}

// Map from chain name to its upgrade json contents.
func WithUpgradeConfigs(upgradeConfigs map[string]string) OpOption {
	return func(op *Op) {
		op.upgradeConfigs = upgradeConfigs
	}
}

// Map from subnet id to its configuration json contents.
func WithSubnetConfigs(subnetConfigs map[string]string) OpOption {
	return func(op *Op) {
		op.subnetConfigs = subnetConfigs
	}
}

// Map from node name to its custom node config
func WithCustomNodeConfigs(customNodeConfigs map[string]string) OpOption {
	return func(op *Op) {
		op.customNodeConfigs = customNodeConfigs
	}
}

func WithNumSubnets(numSubnets uint32) OpOption {
	return func(op *Op) {
		op.numSubnets = numSubnets
	}
}

func WithReassignPortsIfUsed(reassignPortsIfUsed bool) OpOption {
	return func(op *Op) {
		op.reassignPortsIfUsed = reassignPortsIfUsed
	}
}

func WithDynamicPorts(dynamicPorts bool) OpOption {
	return func(op *Op) {
		op.dynamicPorts = dynamicPorts
	}
}

func WithWalletPrivateKey(walletPrivateKey string) OpOption {
	return func(op *Op) {
		op.walletPrivateKey = walletPrivateKey
	}
}

func WithCustomNetworkGenesisPath(customNetworkGenesisPath string) OpOption {
	return func(op *Op) {
		op.customNetworkGenesisPath = customNetworkGenesisPath
	}
}

func WithCustomNetworkBootstrapNodeIDs(customNetworkBootstrapNodeIDs []string) OpOption {
	return func(op *Op) {
		op.customNetworkBootstrapNodeIDs = customNetworkBootstrapNodeIDs
	}
}

func WithCustomNetworkBootstrapNodeIPPortPairs(customNetworkBootstrapNodeIPPortPairs []string) OpOption {
	return func(op *Op) {
		op.customNetworkBootstrapNodeIPPortPairs = customNetworkBootstrapNodeIPPortPairs
	}
}

func WithUpgradePath(upgradePath string) OpOption {
	return func(op *Op) {
		op.upgradePath = upgradePath
	}
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
