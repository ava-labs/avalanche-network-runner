package local

import (
	"context"
	"crypto"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
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
	"github.com/ava-labs/avalanchego/snow/uptime"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/upgrade"
	"github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
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
	// The Public IP
	publicIP string
	// The API port
	apiPort uint16
	// The P2P (staking) port
	p2pPort uint16
	// Returns a connection to this node
	getConnFunc getConnFunc
	// The data dir of the node
	dataDir string
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
	// signals that the process is stopped but the information is valid
	// and can be resumed
	paused bool
	// if set, returns 0.0.0.0 if httpHost setting is public
	zeroIP bool
}

func defaultGetConnFunc(ctx context.Context, node node.Node) (net.Conn, error) {
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, constants.NetworkType, net.JoinHostPort(node.GetIP(), fmt.Sprintf("%d", node.GetP2PPort())))
}

// AttachPeer: see Network
func (node *localNode) AttachPeer(ctx context.Context, router router.InboundHandler) (peer.Peer, error) {
	tlsCert, err := staking.NewTLSCert()
	if err != nil {
		return nil, err
	}
	tlsConfg := peer.TLSConfig(*tlsCert, nil)
	clientUpgrader := peer.NewTLSClientUpgrader(
		tlsConfg,
		prometheus.NewCounter(prometheus.CounterOpts{}),
	)
	conn, err := node.getConnFunc(ctx, node)
	if err != nil {
		return nil, err
	}
	peerID, conn, cert, err := clientUpgrader.Upgrade(conn)
	if err != nil {
		return nil, err
	}
	mc, err := message.NewCreator(
		logging.NoLog{},
		prometheus.NewRegistry(),
		constants.DefaultNetworkCompressionType,
		10*time.Second,
	)
	if err != nil {
		return nil, err
	}

	metrics, err := peer.NewMetrics(
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
	signerIP := utils.NewAtomic(netip.AddrPortFrom(
		netip.IPv6Loopback(),
		1,
	))
	tls := tlsCert.PrivateKey.(crypto.Signer)
	bls0, err := bls.NewSigner()
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
		VersionCompatibility: version.GetCompatibility(upgrade.InitiallyActiveTime),
		MySubnets:            set.Set[ids.ID]{},
		Beacons:              validators.NewManager(),
		NetworkID:            node.networkID,
		PingFrequency:        constants.DefaultPingFrequency,
		PongTimeout:          constants.DefaultPingPongTimeout,
		MaxClockDifference:   time.Minute,
		ResourceTracker:      resourceTracker,
		UptimeCalculator:     uptime.NoOpCalculator,
		IPSigner:             peer.NewIPSigner(signerIP, tls, bls0),
		SupportedACPs:        []uint32{},
		ObjectedACPs:         []uint32{},
	}

	p := peer.Start(
		config,
		conn,
		cert,
		peerID,
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
func (node *localNode) GetIP() string {
	if node.zeroIP && (node.httpHost == "0.0.0.0" || node.httpHost == ".") {
		return "0.0.0.0"
	}
	return node.publicIP
}

// See node.Node
func (node *localNode) GetURI() string {
	return fmt.Sprintf("http://%s:%d", node.GetIP(), node.GetAPIPort())
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
func (node *localNode) GetDataDir() string {
	return node.dataDir
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

// See node.Node
func (node *localNode) GetPaused() bool {
	return node.paused
}
