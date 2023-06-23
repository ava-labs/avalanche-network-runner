package local

import (
	"context"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
)

func NewAttachedNetwork(
	log logging.Logger,
	avalancheOpsYaml string,
) (network.Network, error) {
	net, err := newNetwork(
		log,
		api.NewAPIClient,
		&nodeProcessCreator{
			colorPicker: utils.NewColorPicker(),
			log:         log,
			stdout:      os.Stdout,
			stderr:      os.Stderr,
		},
		"",
		"",
		false,
	)
	if err != nil {
		return net, err
	}
	err = net.attach(
		context.Background(),
		avalancheOpsYaml,
	)
	return net, err
}

func (ln *localNetwork) attach(
	ctx context.Context,
	avalancheOpsYaml string,
) error {
	ln.lock.Lock()
	defer ln.lock.Unlock()
	var avalancheOpsData map[string]interface{}
	err := yaml.Unmarshal([]byte(avalancheOpsYaml), &avalancheOpsData)
	if err != nil {
		return err
	}
	createdNodes := avalancheOpsData["resource"].(map[string]interface{})["created_nodes"].([]interface{})
	for _, node := range createdNodes {
		nodeMap := node.(map[string]interface{})
		machineId := nodeMap["machineId"].(string)
		nodeIDStr := nodeMap["nodeId"].(string)
		nodeID, err := ids.NodeIDFromString(nodeIDStr)
		if err != nil {
			return err
		}
		publicIp := nodeMap["publicIp"].(string)
		httpEndpoint := nodeMap["httpEndpoint"].(string)
		apiPortStr := strings.Split(httpEndpoint, ":")[2]
		apiPort64, err := strconv.ParseUint(apiPortStr, 10, 16)
		if err != nil {
			return err
		}
		apiPort := uint16(apiPort64)
		nodeProcess, err := newFakeNodeProcess(machineId, ln.log)
		if err != nil {
			return err
		}
		node := &localNode{
			name:    machineId,
			nodeID:  nodeID,
			client:  ln.newAPIClientF(publicIp, apiPort),
			apiPort: apiPort,
			process: nodeProcess,
			IP:      publicIp,
		}
		ln.nodes[machineId] = node
	}
	return nil
}
