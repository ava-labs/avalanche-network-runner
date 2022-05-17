package local

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/network/throttling"
	"github.com/ava-labs/avalanchego/snow/networking/router"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/staking"
	avago_utils "github.com/ava-labs/avalanchego/utils"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/version"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shirou/gopsutil/process"
)

// interface compliance
var (
	_ node.Node   = (*localNode)(nil)
	_ NodeProcess = (*nodeProcessImpl)(nil)
	_ getConnFunc = defaultGetConnFunc
)

type getConnFunc func(context.Context, node.Node) (net.Conn, error)

// NodeProcess as an interface so we can mock running
// AvalancheGo binaries in tests
// Expected call sequence: instantiation, Start, Stop, Wait
type NodeProcess interface {
	// Start this process
	// Returns error if not called after instantiation
	Start(chan network.UnexpectedNodeStopMsg) error
	// Send a SIGTERM to this process
	// Returns error if not called after Start
	Stop(ctx context.Context) error
	// Returns when the process finishes exiting
	// Returns error if not called after Start/Stop
	Wait() error
	// Returns if the process is executing
	// Returns false if Start was not called
	Alive() bool
}

type nodeProcessImpl struct {
	name             string
	lock             sync.RWMutex
	cmd              *exec.Cmd
	waitReturnCh     chan error
	unexpectedStopCh chan network.UnexpectedNodeStopMsg
	state            int
	closeOnStop      chan struct{}
}

const (
	Initial = iota
	Started
	Stopping
	Stopped
	Waited
)

// to be called only on Initial state
func (p *nodeProcessImpl) Start(unexpectedStopCh chan network.UnexpectedNodeStopMsg) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state != Initial {
		return errors.New("start called on invalid state")
	}
	p.unexpectedStopCh = unexpectedStopCh
	if err := p.cmd.Start(); err != nil {
		return err
	}
	p.state = Started
	p.waitReturnCh = make(chan error, 1)
	p.closeOnStop = make(chan struct{})
	go func() {
		p.waitReturnCh <- p.cmd.Wait()
		p.lock.Lock()
		state := p.state
		p.state = Stopped
		close(p.closeOnStop)
		p.lock.Unlock()
		if state != Stopping {
			p.unexpectedStopCh <- network.UnexpectedNodeStopMsg{
				Name:     p.name,
				ExitCode: p.cmd.ProcessState.ExitCode(),
			}
		}
	}()
	return nil
}

func (p *nodeProcessImpl) Wait() error {
	p.lock.RLock()
	state := p.state
	p.lock.RUnlock()
	if state != Started && state != Stopping && state != Stopped {
		return errors.New("wait called on invalid state")
	}
	waitReturn := <-p.waitReturnCh
	p.lock.Lock()
	defer p.lock.Unlock()
	p.state = Waited
	return waitReturn
}

// if context is cancelled, assumes a failure in termination
// and uses SIGKILL over process and descendants
func (p *nodeProcessImpl) Stop(ctx context.Context) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state != Started {
		return errors.New("stop called on invalid state")
	}
	stopResult := p.cmd.Process.Signal(syscall.SIGTERM)
	p.state = Stopping
	go func() {
		select {
		case <-ctx.Done():
			_ = killDescendants(int32(p.cmd.Process.Pid))
			_ = p.cmd.Process.Signal(syscall.SIGKILL)
		case <-p.closeOnStop:
		}
	}()
	return stopResult
}

func (p *nodeProcessImpl) Alive() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.state == Started || p.state == Stopping
}

// Gives access to basic node info, and to most avalanchego apis
type localNode struct {
	// Must be unique across all nodes in this network.
	name string
	// [nodeID] is this node's Avalannche Node ID.
	// Set in network.AddNode
	nodeID ids.ShortID
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
	tlsConfg := peer.TLSConfig(*tlsCert)
	clientUpgrader := peer.NewTLSClientUpgrader(tlsConfg)
	conn, err := node.getConnFunc(ctx, node)
	if err != nil {
		return nil, err
	}
	mc, err := message.NewCreator(
		prometheus.NewRegistry(),
		true,
		"",
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
	ip := avago_utils.IPDesc{
		IP:   net.IPv6zero,
		Port: 0,
	}
	config := &peer.Config{
		Metrics:              metrics,
		MessageCreator:       mc,
		Log:                  logging.NoLog{},
		InboundMsgThrottler:  throttling.NewNoInboundThrottler(),
		OutboundMsgThrottler: throttling.NewNoOutboundThrottler(),
		Network: peer.NewTestNetwork(
			mc,
			node.networkID,
			ip,
			version.CurrentApp,
			tlsCert.PrivateKey.(crypto.Signer),
			ids.Set{},
			100,
		),
		Router:               router,
		VersionCompatibility: version.GetCompatibility(node.networkID),
		VersionParser:        version.NewDefaultApplicationParser(),
		MySubnets:            ids.Set{},
		Beacons:              validators.NewSet(),
		NetworkID:            node.networkID,
		PingFrequency:        constants.DefaultPingFrequency,
		PongTimeout:          constants.DefaultPingPongTimeout,
		MaxClockDifference:   time.Minute,
	}
	_, conn, cert, err := clientUpgrader.Upgrade(conn)
	if err != nil {
		return nil, err
	}

	p := peer.Start(
		config,
		conn,
		cert,
		peer.CertToID(tlsCert.Leaf),
	)
	if err != nil {
		return nil, err
	}

	return p, nil
}

// See node.Node
func (node *localNode) GetName() string {
	return node.name
}

// See node.Node
func (node *localNode) GetNodeID() ids.ShortID {
	return node.nodeID
}

// See node.Node
func (node *localNode) GetAPIClient() api.Client {
	return node.client
}

// See node.Node
func (node *localNode) GetURL() string {
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

func (node *localNode) Alive() bool {
	return node.process.Alive()
}

func killDescendants(pid int32) error {
	procs, err := process.Processes()
	if err != nil {
		return err
	}
	for _, proc := range procs {
		ppid, err := proc.Ppid()
		if err != nil {
			return err
		}
		if ppid != pid {
			continue
		}
		if err := killDescendants(proc.Pid); err != nil {
			return err
		}
		_ = syscall.Kill(int(proc.Pid), syscall.SIGKILL)
	}
	return nil
}
