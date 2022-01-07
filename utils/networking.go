package utils

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

// AssignAvailablePort generates a random port number and then
// verifies it is actually free. If it is, returns that port,
// otherwise retries.
// To avoid an endless loop scenario it has a timeout check
func AssignAvailablePort() (uint16, error) {
	ctx, cancel := context.WithTimeout(context.Background(), netListenTimeout)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
			port := rand.Intn(maxPort-minPort+1) + minPort
			l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
			if err == nil {
				port := uint16(l.Addr().(*net.TCPAddr).Port)
				_ = l.Close()
				return port, nil
			}
		}
	}
}
