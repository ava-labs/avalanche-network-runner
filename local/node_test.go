package local

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/network/peer"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/wrappers"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func upgradeConn(myTLSCert *tls.Certificate, conn net.Conn) (ids.ShortID, net.Conn, error) {
	tlsConfig := peer.TLSConfig(*myTLSCert)
	upgrader := peer.NewTLSServerUpgrader(tlsConfig)
	// this will block until the ssh handshake is done
	peerID, tlsConn, _, err := upgrader.Upgrade(conn)
	return peerID, tlsConn, err
}

// verifyProtocol reads from the connection and asserts that we read the expected message sequence.
// If an unexpected error occurs, or we get an unexpected message, sends an error on [errCh].
// Sends nil on [errCh] if we get the expected message sequence.
func verifyProtocol(
	t *testing.T,
	assert *assert.Assertions,
	opSequence []message.Op,
	mc message.Creator,
	conn net.Conn,
	errCh chan error,
) {
	// Do the TLS handshake on [conn]
	myTLSCert, err := staking.NewTLSCert()
	if err != nil {
		errCh <- err
		return
	}
	peerID, tlsConn, err := upgradeConn(myTLSCert, conn)
	if err != nil {
		errCh <- err
		return
	}
	conn = tlsConn

	/* TODO
	// Send the peer our version and peerlist
		myIP := avago_utils.IPDesc{
			IP:   net.IPv6zero,
			Port: 0,
		}
		mc.Version(
			constants.MainnetID,
			time.Now(),
			myIP,
			version.CurrentApp.String(),
		)
	*/

	for _, expectedOpMsg := range opSequence {
		msgLenBytes := &bytes.Buffer{}
		// read the message length
		if _, err := io.CopyN(msgLenBytes, conn, wrappers.IntLen); err != nil {
			errCh <- err
			_ = conn.Close()
			return
		}
		msgLen := binary.BigEndian.Uint32(msgLenBytes.Bytes())
		msgBytes := &bytes.Buffer{}
		// read the message
		if _, err := io.CopyN(msgBytes, conn, int64(msgLen)); err != nil {
			errCh <- err
			_ = conn.Close()
			return
		}
		msg, err := mc.Parse(msgBytes.Bytes(), peerID, func() {})
		assert.NoError(err)
		op := msg.Op()
		assert.Equal(expectedOpMsg, op)
	}
	// signal we are actually done
	errCh <- nil
}

// TestAttachPeer tests that we can attach a test peer to a node
// and that the node receives messages sent through the test peer
func TestAttachPeer(t *testing.T) {
	assert := assert.New(t)

	// [nodeConn] is the connection that [node] uses to read from/write to [peer] (defined below)
	// Similar for [peerConn].
	nodeConn, peerConn := net.Pipe()

	node := localNode{
		nodeID:    ids.GenerateTestShortID(),
		networkID: constants.MainnetID,
		getConnFunc: func(ctx context.Context, n node.Node) (net.Conn, error) {
			return peerConn, nil
		},
	}

	// For message creation and parsing
	mc, err := message.NewCreator(
		prometheus.NewRegistry(),
		true,
		"",
		10*time.Second,
	)
	assert.NoError(err)

	expectedMessages := []message.Op{
		message.Version,
		message.Chits,
	}

	// [p] define below will write to/read from [peerConn]
	// Start a goroutine that reads messages from the other end of that
	// connection and asserts that we get the expected messages
	errCh := make(chan error, 1)
	go verifyProtocol(t, assert, expectedMessages, mc, nodeConn, errCh)

	// attach a test peer to [node]
	handler := &noOpInboundHandler{}
	p, err := node.AttachPeer(context.Background(), handler)
	assert.NoError(err)

	/* TODO
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = p.AwaitReady(ctx)
	assert.NoError(err)
	*/

	// we'll use a Chits message for testing. (We could use any message type.)
	containerIDs := []ids.ID{
		ids.GenerateTestID(),
		ids.GenerateTestID(),
		ids.GenerateTestID(),
	}
	requestID := uint32(42)
	chainID := constants.PlatformChainID
	// create the Chits message
	msg, err := mc.Chits(chainID, requestID, containerIDs)
	assert.NoError(err)
	// send chits to [node]
	ok := p.Send(msg)
	assert.True(ok)
	// wait until the go routines are done
	// also ensures that [assert] calls will be reflected in test results if failed
	assert.NoError(<-errCh)
}
