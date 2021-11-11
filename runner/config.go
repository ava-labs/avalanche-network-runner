package runner

import (
	"fmt"
	"os"

	"github.com/ava-labs/avalanche-network-runner/constants"
	"github.com/ava-labs/avalanche-network-runner/k8s"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/logging"
)

type NodeCommonConfig struct {
	NodeConfFile string `json:"nodeConfFile"`
	GenesisFile  string `json:"genesisFile"`
	NodeCount    int    `json:"nodeCount"`
}

type NodeCustomConfig struct {
	K8sSpec        k8s.Spec
	NodeConfigFile string `json:"nodeConfigFile"`
	Name           string `json:"name"`
	CertFile       string `json:"certFile"`
	KeyFile        string `json:"keyFile"`
}

type RunnerConfig struct {
	NodeCommonConfig NodeCommonConfig   `json:"avalanchegoCommon"`
	NodeCustomConfig []NodeCustomConfig `json:"nodeCustom"`
	K8sCommon        k8s.Spec           `json:"k8sCommon"`
}

func LoadConfig(log logging.Logger, rconfig RunnerConfig, configDir string) (network.Config, error) {
	var err error
	var cert, key, nodeConfigFile []byte

	k8sCommon := rconfig.K8sCommon
	commonCfg := rconfig.NodeCommonConfig
	netCfg := network.Config{}

	if commonCfg.GenesisFile != "" {
		netCfg.Genesis, err = os.ReadFile(fmt.Sprintf("%s%s", configDir, commonCfg.GenesisFile))
		if err != nil {
			return network.Config{}, err
		}
	} else {
		netCfg.Genesis, netCfg.NodeConfigs, err = GenerateDefaultGenesis(log, constants.DefaultNetworkID, rconfig.NodeCommonConfig.NodeCount)
		if err != nil {
			return network.Config{}, err
		}
		for i, _ := range netCfg.NodeConfigs {
			netCfg.NodeConfigs[i].ImplSpecificConfig = k8sCommon
		}
	}
	if commonCfg.NodeConfFile != "" {
		nodeConfigFile, err = os.ReadFile(fmt.Sprintf("%s%s", configDir, commonCfg.NodeConfFile))
		if err != nil {
			return network.Config{}, err
		}
	}

	if len(rconfig.NodeCustomConfig) > 0 {
		netCfg.NodeConfigs = make([]node.Config, 0)

		for i, k := range rconfig.NodeCustomConfig {
			k8sCustom := overrideCommon(k8sCommon, k.K8sSpec)
			if k.KeyFile != "" && k.CertFile != "" {
				cert, err = os.ReadFile(fmt.Sprintf("%s%s", configDir, k.CertFile))
				if err != nil {
					return network.Config{}, err
				}
				key, err = os.ReadFile(fmt.Sprintf("%s%s", configDir, k.KeyFile))
				if err != nil {
					return network.Config{}, err
				}
			} else {
				cert, key, err = staking.NewCertAndKeyBytes()
				if err != nil {
					return network.Config{}, err
				}
			}
			if k.NodeConfigFile != "" {
				nodeConfigFile, err = os.ReadFile(fmt.Sprintf("%s/%s", configDir, k.NodeConfigFile))
				if err != nil {
					return network.Config{}, err
				}

			}
			log.Info("loading config %d", i)
			c := node.Config{
				Name:               k.Name,
				StakingCert:        cert,
				StakingKey:         key,
				ConfigFile:         nodeConfigFile,
				ImplSpecificConfig: k8sCustom,
			}
			if i == 0 {
				c.IsBeacon = true
			}
			netCfg.NodeConfigs = append(netCfg.NodeConfigs, c)
		}
	}
	return netCfg, nil
}

func overrideCommon(k8sCommon k8s.Spec, k8sCustom k8s.Spec) k8s.Spec {
	override := k8sCommon
	if k8sCustom.APIVersion != "" {
		override.APIVersion = k8sCustom.APIVersion
	}
	if k8sCustom.Image != "" {
		override.Image = k8sCustom.Image
	}
	if k8sCustom.Tag != "" {
		override.Tag = k8sCustom.Tag
	}

	return override
}
