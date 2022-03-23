package local

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

// pipedGetConnFunc returns a node.GetConnFunc which when running:
// * returns a piped net.Conn to be used for the test connection
// * runs a go routine which upgrades the connection to TLS
// * runs a second go routine which mocks the node's message endpoint handling messages
func pipedGetConnFunc(assert *assert.Assertions, errc chan error) node.GetConnFunc {
	return func(ctx context.Context, node node.Node) (net.Conn, error) {
		// a mocked connection
		connRead, connWrite := net.Pipe()
		// we first need to upgrade the TLS connection before we can handle other messages
		upgraded := make(chan struct{})

		// upgrade to TLS and then signal that we can proceed handling messages
		go func() {
			tlsCert, err := staking.NewTLSCert()
			assert.NoError(err)

			tlsConfig := peer.TLSConfig(*tlsCert)
			upgrader := peer.NewTLSServerUpgrader(tlsConfig)
			// this will block until the ssh handshake is done
			_, tlsConn, _, err := upgrader.Upgrade(connRead)
			assert.NoError(err)
			connRead = tlsConn
			// signal we can now handle normal connections
			upgraded <- struct{}{}
		}()

		// handle messages after handshake
		go func() {
			select {
			// give the chance to be aborted
			case <-ctx.Done():
				errc <- ctx.Err()
				// wait until the handshake is done
			case <-upgraded:
			}

			// sequence of messages we expect
			opSequence := []message.Op{
				message.Version,
				message.Chits,
			}
			// verify the sequence is correct
			verifyProtocol(assert, opSequence, connRead, node.GetNodeID(), errc)
		}()
		return connWrite, nil
	}
}

// verifyProtocol reads from the connection and verifies we get the expected message sequence
func verifyProtocol(assert *assert.Assertions, opSequence []message.Op, connRead net.Conn, nodeID ids.ShortID, errc chan error) {
	// needed for message parsing
	mc, err := message.NewCreator(
		prometheus.NewRegistry(),
		true,
		"",
		10*time.Second,
	)
	assert.NoError(err)
	msgLenBytes := make([]byte, wrappers.IntLen)

	for _, expectedOpMsg := range opSequence {
		// read the message length
		_, err := connRead.Read(msgLenBytes)
		if err != nil {
			errc <- err
			_ = connRead.Close()
			return
		}
		msgLen := binary.BigEndian.Uint32(msgLenBytes)
		msgBytes := make([]byte, msgLen)
		// read the message
		_, err = connRead.Read(msgBytes)
		if err != nil {
			errc <- err
			_ = connRead.Close()
			return
		}
		// readBytes = nil
		msg, err := mc.Parse(msgBytes, nodeID, func() {
		})
		assert.NoError(err)
		op := msg.Op()
		assert.Equal(expectedOpMsg, op)
	}
	// signal we are actually done (will also ensure that `assert` calls will be reflected in test results if failed)
	errc <- nil
}

// TestAttachPeer tests that we can attach a test peer to a node
func TestAttachPeer(t *testing.T) {
	assert := assert.New(t)
	// set up the network
	networkConfig := testNetworkConfig(t)
	net, err := newNetwork(logging.NoLog{}, networkConfig, newMockAPISuccessful, &localTestSuccessfulNodeProcessCreator{}, "")
	assert.NoError(err)
	assert.NoError(awaitNetworkHealthy(net, defaultHealthyTimeout))

	// get the first node
	names, err := net.GetNodeNames()
	assert.NoError(err)
	nodeName := names[0]
	// we'll attach to this one
	attachToNode, err := net.GetNode(nodeName)
	assert.NoError(err)

	// cast so we can use a custom GetConnFunc
	originalNode, ok := attachToNode.(*localNode)
	assert.True(ok)

	errCh := make(chan error)
	// assign our own pipedGetConnFunc so we can mock the node
	originalNode.getConnFunc = pipedGetConnFunc(assert, errCh)
	// now attach the peer to the node
	handler := &noOpInboundHandler{}

	// context for timeout control (give enough time)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := attachToNode.AttachPeer(ctx, handler)
	assert.NoError(err)

	// we'll use a Chits message for testing
	// it could be any message type really
	mc, err := message.NewCreator(
		prometheus.NewRegistry(),
		true,
		"",
		10*time.Second,
	)
	assert.NoError(err)
	containerIDs := []ids.ID{
		ids.GenerateTestID(),
		ids.GenerateTestID(),
		ids.GenerateTestID(),
	}
	requestID := uint32(42)
	chainID := constants.PlatformChainID
	// create the Chits message and...
	msg, err := mc.Chits(chainID, requestID, containerIDs)
	assert.NoError(err)
	// ... send
	ok = p.Send(msg)
	assert.True(ok)
	// wait until the go routines are done (will also ensure that `assert` calls will be reflected in test results if failed)
	assert.NoError(<-errCh)
}
