package local

import (
	"context"
	"os"
	"os/exec"
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
	regionalResources := avalancheOpsData["resource"].(map[string]interface{})["regional_resources"].(map[string]interface{})
	sshCmds := map[string]string{}
	for _, resource := range regionalResources {
		sshCommandsPathAnchorNodesPath := resource.(map[string]interface{})["ssh_commands_path_anchor_nodes"].(string)
		sshCommandsPathNonAnchorNodesPath := resource.(map[string]interface{})["ssh_commands_path_non_anchor_nodes"].(string)
		sshCommandsPathAnchorNodesBytes, err := os.ReadFile(sshCommandsPathAnchorNodesPath)
		if err != nil {
			return err
		}
		sshCommandsPathNonAnchorNodesBytes, err := os.ReadFile(sshCommandsPathNonAnchorNodesPath)
		if err != nil {
			return err
		}
		sshCommandsPathAnchorNodes := string(sshCommandsPathAnchorNodesBytes)
		for _, line := range strings.Split(sshCommandsPathAnchorNodes, "\n") {
			if strings.HasPrefix(line, "ssh ") {
				cmdParts := strings.Fields(line)
				if len(cmdParts) == 7 {
					ip := strings.Split(cmdParts[6], "@")[1]
					sshCmds[ip] = line
				}
			}
		}
		sshCommandsPathNonAnchorNodes := string(sshCommandsPathNonAnchorNodesBytes)
		for _, line := range strings.Split(sshCommandsPathNonAnchorNodes, "\n") {
			if strings.HasPrefix(line, "ssh ") {
				cmdParts := strings.Fields(line)
				if len(cmdParts) == 7 {
					ip := strings.Split(cmdParts[6], "@")[1]
					sshCmds[ip] = line
				}
			}
		}
	}
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
			ssh:     sshCmds[publicIp],
		}
		ln.nodes[machineId] = node
	}
	return nil
}

func getBaseSshCmd(cmdLine string) (string, []string) {
	cmd := ""
	args := []string{}
	for i, ws := range strings.Split(cmdLine, "\"") {
		if i%2 == 0 {
			for _, w := range strings.Fields(ws) {
				if cmd == "" {
					cmd = w
				} else {
					args = append(args, w)
				}
			}
		} else {
			args = append(args, ws)
		}
	}
	return cmd, args
}

func execSshCmd(baseSsh string, remoteCmd string) (string, error) {
	cmd, args := getBaseSshCmd(baseSsh)
	args = append(args, remoteCmd)
	exeCmd := exec.Command(cmd, args...)
	out, err := exeCmd.CombinedOutput()
	return string(out), err
}
