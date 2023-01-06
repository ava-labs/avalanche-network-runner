package local

import (
	"context"
	"crypto"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/network/node/status"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/network/throttling"
	"github.com/ava-labs/avalanchego/snow/networking/router"
	"github.com/ava-labs/avalanchego/snow/networking/tracker"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/ips"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/math/meter"
	"github.com/ava-labs/avalanchego/utils/resource"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/version"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	_ getConnFunc = defaultGetConnFunc
	_ node.Node   = (*localNode)(nil)
)

type getConnFunc func(context.Context, node.Node) (net.Conn, error)

const (
	peerMsgQueueBufferSize      = 1024
	peerResourceTrackerDuration = 10 * time.Second
	peerStartWaitTimeout        = 30 * time.Second
)

// Gives access to basic node info, and to most avalanchego apis
type localNode struct {
	// Must be unique across all nodes in this network.
	name string
	// [nodeID] is this node's Avalannche Node ID.
	// Set in network.AddNode
	nodeID ids.NodeID
	// The ID of the network this node exists in
	networkID uint32
	// Allows user to make API calls to this node.
	client api.Client
	// The process running this node.
	process NodeProcess
	// The API port
	apiPort uint16
	// The P2P (staking) port
	p2pPort uint16
	// Returns a connection to this node
	getConnFunc getConnFunc
	// The db dir of the node
	dbDir string
	// The logs dir of the node
	logsDir string
	// The plugin dir of the node
	pluginDir string
	// The node config
	config node.Config
	// The node httpHost
	httpHost string
	// maps from peer ID to peer object
	attachedPeers map[string]peer.Peer
}

func defaultGetConnFunc(ctx context.Context, node node.Node) (net.Conn, error) {
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, constants.NetworkType, net.JoinHostPort(node.GetURL(), fmt.Sprintf("%d", node.GetP2PPort())))
}

// AttachPeer: see Network
func (node *localNode) AttachPeer(ctx context.Context, router router.InboundHandler) (peer.Peer, error) {
	tlsCert, err := staking.NewTLSCert()
	if err != nil {
		return nil, err
	}
	tlsConfg := peer.TLSConfig(*tlsCert, nil)
	clientUpgrader := peer.NewTLSClientUpgrader(tlsConfg)
	conn, err := node.getConnFunc(ctx, node)
	if err != nil {
		return nil, err
	}
	mc, err := message.NewCreator(
		prometheus.NewRegistry(),
		"",
		true,
		10*time.Second,
	)
	if err != nil {
		return nil, err
	}

	metrics, err := peer.NewMetrics(
		logging.NoLog{},
		"",
		prometheus.NewRegistry(),
	)
	if err != nil {
		return nil, err
	}
	resourceTracker, err := tracker.NewResourceTracker(
		prometheus.NewRegistry(),
		resource.NoUsage,
		meter.ContinuousFactory{},
		peerResourceTrackerDuration,
	)
	if err != nil {
		return nil, err
	}
	signerIP := ips.NewDynamicIPPort(net.IPv6zero, 0)
	tls := tlsCert.PrivateKey.(crypto.Signer)
	gossipTracker, err := peer.NewGossipTracker(prometheus.NewRegistry(), "anr")
	if err != nil {
		return nil, err
	}
	config := &peer.Config{
		Metrics:              metrics,
		MessageCreator:       mc,
		Log:                  logging.NoLog{},
		InboundMsgThrottler:  throttling.NewNoInboundThrottler(),
		Network:              peer.TestNetwork,
		Router:               router,
		VersionCompatibility: version.GetCompatibility(node.networkID),
		MySubnets:            set.Set[ids.ID]{},
		Beacons:              validators.NewSet(),
		NetworkID:            node.networkID,
		PingFrequency:        constants.DefaultPingFrequency,
		PongTimeout:          constants.DefaultPingPongTimeout,
		MaxClockDifference:   time.Minute,
		ResourceTracker:      resourceTracker,
		GossipTracker:        gossipTracker,
		IPSigner:             peer.NewIPSigner(signerIP, tls),
	}
	_, conn, cert, err := clientUpgrader.Upgrade(conn)
	if err != nil {
		return nil, err
	}

	p := peer.Start(
		config,
		conn,
		cert,
		ids.NodeIDFromCert(tlsCert.Leaf),
		peer.NewBlockingMessageQueue(
			config.Metrics,
			logging.NoLog{},
			peerMsgQueueBufferSize,
		),
	)
	cctx, cancel := context.WithTimeout(ctx, peerStartWaitTimeout)
	err = p.AwaitReady(cctx)
	cancel()
	if err != nil {
		return nil, err
	}

	node.attachedPeers[p.ID().String()] = p
	return p, nil
}

func (node *localNode) SendOutboundMessage(ctx context.Context, peerID string, content []byte, op uint32) (bool, error) {
	attachedPeer, ok := node.attachedPeers[peerID]
	if !ok {
		return false, fmt.Errorf("peer with ID %s is not attached here", peerID)
	}
	msg := NewTestMsg(message.Op(op), content, false)
	return attachedPeer.Send(ctx, msg), nil
}

// See node.Node
func (node *localNode) GetName() string {
	return node.name
}

// See node.Node
func (node *localNode) GetNodeID() ids.NodeID {
	return node.nodeID
}

// See node.Node
func (node *localNode) GetAPIClient() api.Client {
	return node.client
}

// See node.Node
func (node *localNode) GetURL() string {
	if node.httpHost == "0.0.0.0" || node.httpHost == "." {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// See node.Node
func (node *localNode) GetP2PPort() uint16 {
	return node.p2pPort
}

// See node.Node
func (node *localNode) GetAPIPort() uint16 {
	return node.apiPort
}

func (node *localNode) Status() status.Status {
	return node.process.Status()
}

// See node.Node
func (node *localNode) GetBinaryPath() string {
	return node.config.BinaryPath
}

// See node.Node
func (node *localNode) GetPluginDir() string {
	return node.pluginDir
}

// See node.Node
// TODO rename method so linter doesn't complain.
func (node *localNode) GetDbDir() string { //nolint
	return node.dbDir
}

// See node.Node
func (node *localNode) GetLogsDir() string {
	return node.logsDir
}

// See node.Node
func (node *localNode) GetConfigFile() string {
	return node.config.ConfigFile
}

// See node.Node
func (node *localNode) GetConfig() node.Config {
	return node.config
}

// See node.Node
func (node *localNode) GetFlag(k string) (string, error) {
	var v string
	if node.config.ConfigFile != "" {
		var configFileMap map[string]interface{}
		if err := json.Unmarshal([]byte(node.config.ConfigFile), &configFileMap); err != nil {
			return "", err
		}
		vIntf, ok := configFileMap[k]
		if ok {
			v, ok = vIntf.(string)
			if !ok {
				return "", fmt.Errorf("unexpected type for %q expected string got %T", k, vIntf)
			}
		}
	} else if node.config.Flags != nil {
		vIntf, ok := node.config.Flags[k]
		if ok {
			v, ok = vIntf.(string)
			if !ok {
				return "", fmt.Errorf("unexpected type for %q expected string got %T", k, vIntf)
			}
		}
	}
	return v, nil
}
