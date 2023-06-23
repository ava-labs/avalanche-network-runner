package local

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/utils"
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
	fmt.Println(avalancheOpsData)
	/*
		node := &localNode{
			name:          nodeConfig.Name,
			nodeID:        nodeID,
			networkID:     ln.networkID,
			client:        ln.newAPIClientF("localhost", nodeData.apiPort),
			process:       nodeProcess,
			apiPort:       nodeData.apiPort,
			p2pPort:       nodeData.p2pPort,
			getConnFunc:   defaultGetConnFunc,
			dataDir:       nodeData.dataDir,
			dbDir:         nodeData.dbDir,
			logsDir:       nodeData.logsDir,
			config:        nodeConfig,
			pluginDir:     nodeData.pluginDir,
			httpHost:      nodeData.httpHost,
			attachedPeers: map[string]peer.Peer{},
		}
		ln.nodes[node.name] = node
	*/
	return nil
}
