package local

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	maxPort          = math.MaxUint16
	minPort          = 10000
	netListenTimeout = 3 * time.Second
)

// getFreePort generates a random port number and then
// verifies it is free. If it is, returns that port, otherwise retries.
// Returns an error if no free port is found within [netListenTimeout].
// Note that it is possible for [getFreePort] to return the same port twice.
func getFreePort() (uint16, error) {
	ctx, cancel := context.WithTimeout(context.Background(), netListenTimeout)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
			// Generate random port in [minPort, maxPort]
			port := uint16(rand.Intn(maxPort-minPort+1) + minPort)
			// Verify it's free by binding to it
			l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
			if err != nil {
				// Couldn't bind to this port. Try another.
				continue
			}
			// We could bind to [port] so must be free.
			_ = l.Close()
			return port, nil
		}
	}
}
