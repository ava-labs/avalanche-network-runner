package local

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/utils/logging"
)

// writeFiles writes the files a node needs on startup.
// It returns flags used to point to those files.
func writeFiles(genesis []byte, nodeRootDir string, nodeConfig *node.Config) ([]string, error) {
	type file struct {
		pathKey   string
		flagValue string
		path      string
		contents  []byte
	}
	files := []file{
		{
			flagValue: filepath.Join(nodeRootDir, stakingKeyFileName),
			path:      filepath.Join(nodeRootDir, stakingKeyFileName),
			pathKey:   config.StakingKeyPathKey,
			contents:  []byte(nodeConfig.StakingKey),
		},
		{
			flagValue: filepath.Join(nodeRootDir, stakingCertFileName),
			path:      filepath.Join(nodeRootDir, stakingCertFileName),
			pathKey:   config.StakingCertPathKey,
			contents:  []byte(nodeConfig.StakingCert),
		},
		{
			flagValue: filepath.Join(nodeRootDir, genesisFileName),
			path:      filepath.Join(nodeRootDir, genesisFileName),
			pathKey:   config.GenesisConfigFileKey,
			contents:  genesis,
		},
	}
	if len(nodeConfig.ConfigFile) != 0 {
		files = append(files, file{
			flagValue: filepath.Join(nodeRootDir, configFileName),
			path:      filepath.Join(nodeRootDir, configFileName),
			pathKey:   config.ConfigFileKey,
			contents:  []byte(nodeConfig.ConfigFile),
		})
	}
	flags := []string{}
	for _, f := range files {
		flags = append(flags, fmt.Sprintf("--%s=%s", f.pathKey, f.flagValue))
		if err := createFileAndWrite(f.path, f.contents); err != nil {
			return nil, fmt.Errorf("couldn't write file at %q: %w", f.path, err)
		}
	}
	if nodeConfig.ChainConfigFiles != nil {
		// only one flag and multiple files
		chainConfigDir := filepath.Join(nodeRootDir, chainConfigSubDir)
		flags = append(flags, fmt.Sprintf("--%s=%s", config.ChainConfigDirKey, chainConfigDir))
		for chainAlias, chainConfigFile := range nodeConfig.ChainConfigFiles {
			chainConfigPath := filepath.Join(chainConfigDir, chainAlias, configFileName)
			if err := createFileAndWrite(chainConfigPath, []byte(chainConfigFile)); err != nil {
				return nil, fmt.Errorf("couldn't write file at %q: %w", chainConfigPath, err)
			}
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
func getPort(
	flags map[string]interface{},
	configFile map[string]interface{},
	portKey string,
) (port uint16, err error) {
	if portIntf, ok := flags[portKey]; ok {
		if portFromFlags, ok := portIntf.(int); ok {
			port = uint16(portFromFlags)
		} else if portFromFlags, ok := portIntf.(float64); ok {
			port = uint16(portFromFlags)
		} else {
			return 0, fmt.Errorf("expected flag %q to be int/float64 but got %T", portKey, portIntf)
		}
	} else if portIntf, ok := configFile[portKey]; ok {
		if portFromConfigFile, ok := portIntf.(float64); ok {
			port = uint16(portFromConfigFile)
		} else {
			return 0, fmt.Errorf("expected flag %q to be float64 but got %T", portKey, portIntf)
		}
	} else {
		// Use a random free port.
		// Note: it is possible but unlikely for getFreePort to return the same port multiple times.
		port, err = getFreePort()
		if err != nil {
			return 0, fmt.Errorf("couldn't get free API port: %w", err)
		}
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
	nodeRootDir := filepath.Join(rootDir, nodeName)
	if err := os.Mkdir(nodeRootDir, 0o755); err != nil {
		if os.IsExist(err) {
			log.Warn("node root directory %s already exists", nodeRootDir)
		} else {
			return "", fmt.Errorf("error creating temp dir: %w", err)
		}
	}
	return nodeRootDir, nil
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
	defer func() {
		_ = file.Close()
	}()
	if _, err := file.Write(contents); err != nil {
		return err
	}
	return nil
}

// addNetworkFlags adds the flags in [networkFlags] to [nodeConfig.Flags].
// [nodeFlags] must not be nil.
func addNetworkFlags(log logging.Logger, networkFlags map[string]interface{}, nodeFlags map[string]interface{}) {
	for flagName, flagVal := range networkFlags {
		// If the same flag is given in network config and node config,
		// the flag in the node config takes precedence
		if val, ok := nodeFlags[flagName]; !ok {
			nodeFlags[flagName] = flagVal
		} else {
			log.Debug(
				"not overwriting node config flag %s (value %v) with network config flag (value %v)",
				flagName, val, flagVal,
			)
		}
	}
}
