package server

import (
	"encoding/json"

	"github.com/ava-labs/avalanche-network-runner/rpcpb"
)

func deepCopy(i *rpcpb.ClusterInfo) (*rpcpb.ClusterInfo, error) {
	b, err := json.Marshal(i)
	if err != nil {
		return nil, err
	}
	var clusterInfo rpcpb.ClusterInfo
	if err := json.Unmarshal(b, &clusterInfo); err != nil {
		return nil, err
	}
	return &clusterInfo, nil
}
