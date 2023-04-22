package local

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"go.uber.org/zap"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	MaxPort          = math.MaxUint16
	minPort          = 10000
	netListenTimeout = 3 * time.Second
)

// isFreePort verifies a given [port] is free
func isFreePort(port uint16) error {
	// Verify it's free by binding to it
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		// Could not bind to [port]. Assumed to be not free.
		return err
	}
	// We could bind to [port] so must be free.
	_ = l.Close()
	return nil
}

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
			port := uint16(rand.Intn(MaxPort-minPort+1) + minPort) //nolint
			if isFreePort(port) != nil {
				// Not free. Try another.
				continue
			}
			return port, nil
		}
	}
}

// writeFiles writes the files a node needs on startup.
// It returns flags used to point to those files.
func writeFiles(networkID uint32, genesis []byte, nodeRootDir string, nodeConfig *node.Config) (map[string]string, error) {
	type file struct {
		pathKey   string
		flagValue string
		path      string
		contents  []byte
	}
	decodedStakingSigningKey, err := base64.StdEncoding.DecodeString(nodeConfig.StakingSigningKey)
	if err != nil {
		return nil, err
	}
	files := []file{
		{
			flagValue: filepath.Join(nodeRootDir, stakingKeyFileName),
			path:      filepath.Join(nodeRootDir, stakingKeyFileName),
			pathKey:   config.StakingTLSKeyPathKey,
			contents:  []byte(nodeConfig.StakingKey),
		},
		{
			flagValue: filepath.Join(nodeRootDir, stakingCertFileName),
			path:      filepath.Join(nodeRootDir, stakingCertFileName),
			pathKey:   config.StakingCertPathKey,
			contents:  []byte(nodeConfig.StakingCert),
		},
		{
			flagValue: filepath.Join(nodeRootDir, stakingSigningKeyFileName),
			path:      filepath.Join(nodeRootDir, stakingSigningKeyFileName),
			pathKey:   config.StakingSignerKeyPathKey,
			contents:  decodedStakingSigningKey,
		},
	}
	if networkID != constants.LocalID {
		files = append(files, file{
			flagValue: filepath.Join(nodeRootDir, genesisFileName),
			path:      filepath.Join(nodeRootDir, genesisFileName),
			pathKey:   config.GenesisConfigFileKey,
			contents:  genesis,
		})
	}
	if len(nodeConfig.ConfigFile) != 0 {
		files = append(files, file{
			flagValue: filepath.Join(nodeRootDir, configFileName),
			path:      filepath.Join(nodeRootDir, configFileName),
			pathKey:   config.ConfigFileKey,
			contents:  []byte(nodeConfig.ConfigFile),
		})
	}
	flags := map[string]string{}
	for _, f := range files {
		flags[f.pathKey] = f.flagValue
		if err := createFileAndWrite(f.path, f.contents); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", f.path, err)
		}
	}
	// chain configs dir
	chainConfigDir := filepath.Join(nodeRootDir, chainConfigSubDir)
	if err := os.MkdirAll(chainConfigDir, 0o750); err != nil {
		return nil, err
	}
	flags[config.ChainConfigDirKey] = chainConfigDir
	// subnet configs dir
	subnetConfigDir := filepath.Join(nodeRootDir, subnetConfigSubDir)
	if err := os.MkdirAll(subnetConfigDir, 0o750); err != nil {
		return nil, err
	}
	flags[config.SubnetConfigDirKey] = subnetConfigDir
	// chain configs
	for chainAlias, chainConfigFile := range nodeConfig.ChainConfigFiles {
		chainConfigPath := filepath.Join(chainConfigDir, chainAlias, configFileName)
		if err := createFileAndWrite(chainConfigPath, []byte(chainConfigFile)); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", chainConfigPath, err)
		}
	}
	// network upgrades
	for chainAlias, chainUpgradeFile := range nodeConfig.UpgradeConfigFiles {
		chainUpgradePath := filepath.Join(chainConfigDir, chainAlias, upgradeConfigFileName)
		if err := createFileAndWrite(chainUpgradePath, []byte(chainUpgradeFile)); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", chainUpgradePath, err)
		}
	}
	// subnet configs
	for subnetID, subnetConfigFile := range nodeConfig.SubnetConfigFiles {
		subnetConfigPath := filepath.Join(subnetConfigDir, subnetID+".json")
		if err := createFileAndWrite(subnetConfigPath, []byte(subnetConfigFile)); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", subnetConfigPath, err)
		}
	}
	return flags, nil
}

// getConfigEntry returns an entry in the config file if it is found, otherwise returns the default value
func getConfigEntry(
	nodeConfigFlags map[string]interface{},
	configFile map[string]interface{},
	flag string,
	defaultVal string,
) (string, error) {
	var entry string
	if val, ok := nodeConfigFlags[flag]; ok {
		if entry, ok := val.(string); ok {
			return entry, nil
		}
		return "", fmt.Errorf("expected node config flag %q to be string but got %T", flag, entry)
	}
	if val, ok := configFile[flag]; ok {
		if entry, ok := val.(string); ok {
			return entry, nil
		}
		return "", fmt.Errorf("expected config file flag %q to be string but got %T", flag, entry)
	}
	return defaultVal, nil
}

// getPort looks up the port config in the config file, if there is none, it tries to get a random free port from the OS
// if [reassingIfUsed] is true, and the port from config is not free, also tries to get a random free port
func getPort(
	flags map[string]interface{},
	configFile map[string]interface{},
	portKey string,
	reassignIfUsed bool,
) (port uint16, err error) {
	if portIntf, ok := flags[portKey]; ok {
		switch gotPort := portIntf.(type) {
		case int:
			port = uint16(gotPort)
		case float64:
			port = uint16(gotPort)
		default:
			return 0, fmt.Errorf("expected flag %q to be int/float64 but got %T", portKey, portIntf)
		}
	} else if portIntf, ok := configFile[portKey]; ok {
		portFromConfigFile, ok := portIntf.(float64)
		if !ok {
			return 0, fmt.Errorf("expected flag %q to be float64 but got %T", portKey, portIntf)
		}
		port = uint16(portFromConfigFile)
	} else {
		// Use a random free port.
		// Note: it is possible but unlikely for getFreePort to return the same port multiple times.
		port, err = getFreePort()
		if err != nil {
			return 0, fmt.Errorf("couldn't get free port: %w", err)
		}
	}
	if reassignIfUsed && isFreePort(port) != nil {
		port, err = getFreePort()
		if err != nil {
			return 0, fmt.Errorf("couldn't get free port: %w", err)
		}
	}
	// last check, avoid starting network with used ports
	if err := isFreePort(port); err != nil {
		return 0, fmt.Errorf("port %d is not free: %w", port, err)
	}
	return port, nil
}

func makeNodeDir(log logging.Logger, rootDir, nodeName string) (string, error) {
	if rootDir == "" {
		log.Warn("no network root directory defined; will create this node's runtime directory in working directory")
	}
	// [nodeRootDir] is where this node's config file, C-Chain config file,
	// staking key, staking certificate and genesis file will be written.
	// (Other file locations are given in the node's config file.)
	// TODO should we do this for other directories? Profiles?
	nodeRootDir := getNodeDir(rootDir, nodeName)
	if err := os.Mkdir(nodeRootDir, 0o755); err != nil {
		if !os.IsExist(err) {
			return "", fmt.Errorf("error creating temp dir %w", err)
		}
		log.Warn("node root directory already exists", zap.String("root-dir", nodeRootDir))
	}
	return nodeRootDir, nil
}

func getNodeDir(rootDir string, nodeName string) string {
	return filepath.Join(rootDir, nodeName)
}

// createFileAndWrite creates a file with the given path and
// writes the given contents
func createFileAndWrite(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(contents)
	return err
}

// addNetworkFlags adds the flags in [networkFlags] to [nodeConfig.Flags].
// [nodeFlags] must not be nil.
func addNetworkFlags(networkFlags map[string]interface{}, nodeFlags map[string]interface{}) {
	for flagName, flagVal := range networkFlags {
		// If the same flag is given in network config and node config,
		// the flag in the node config takes precedence
		if _, ok := nodeFlags[flagName]; !ok {
			nodeFlags[flagName] = flagVal
		}
	}
}
