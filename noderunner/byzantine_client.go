// (c) 2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package noderunner

import (
	"time"

	"github.com/ava-labs/avalanchego/utils/rpc"
)

const conflictingTxs = "conflicting-txs-vertex"

// ByzantineClient for the Avalanche Byzantine API Endpoint
type ByzantineClient struct {
	conflictEngineRequester rpc.EndpointRequester
}

// newByzantineClient returns a new ByzantineClient
func newByzantineClient(uri string, requestTimeout time.Duration) *ByzantineClient {
	return &ByzantineClient{
		conflictEngineRequester: rpc.NewEndpointRequester(uri, "/byzantine", conflictingTxs, requestTimeout),
	}
}

func (c *ByzantineClient) GetIssuedVertices() (int, error) {
	type response struct {
		ConflictingVerticesIssued int
	}

	res := &response{}
	err := c.conflictEngineRequester.SendRequest("issuedVertices", struct{}{}, res)
	if err != nil {
		return 0, err
	}
	return res.ConflictingVerticesIssued, nil
}
