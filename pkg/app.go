package pkg

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/ava-labs/avalanche-network-runner/pkg/models"
	"github.com/ava-labs/avalanche-network-runner/ux"
	"github.com/ava-labs/avalanchego/utils/logging"
)

const (
	BaseDirName                 = ".avalanche-network-runner"
	SubnetDir                   = "subnets"
	ElasticSubnetConfigFileName = "elastic_subnet_config.json"
	DefaultPerms755             = 0o755
	WriteReadReadPerms          = 0o644
)

func GetBaseDir() string {
	// Set base dir
	usr, err := user.Current()
	if err != nil {
		// no logger here yet
		fmt.Printf("unable to get system user %s\n", err)
		return ""
	}
	baseDir := filepath.Join(usr.HomeDir, BaseDirName)

	// Create base dir if it doesn't exist
	err = os.MkdirAll(baseDir, os.ModePerm)
	if err != nil {
		// no logger here yet
		fmt.Printf("failed creating the basedir %s: %s\n", baseDir, err)
		return ""
	}
	return baseDir
}

func GetSubnetDir() string {
	return filepath.Join(GetBaseDir(), SubnetDir)
}

func GetSubnetNameDir(subnet string) string {
	return filepath.Join(GetSubnetDir(), subnet)
}

func GetElasticSubnetConfigPath(subnetName string) string {
	return filepath.Join(GetSubnetDir(), subnetName, ElasticSubnetConfigFileName)
}

func CreateElasticSubnetConfig(subnetName string, es *models.ElasticSubnetConfig) error {
	elasticSubetConfigPath := GetElasticSubnetConfigPath(subnetName)
	if err := os.MkdirAll(filepath.Dir(elasticSubetConfigPath), DefaultPerms755); err != nil {
		return err
	}

	esBytes, err := json.MarshalIndent(es, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(elasticSubetConfigPath, esBytes, WriteReadReadPerms)
}

func LoadElasticSubnetConfig(subnetName string) (models.ElasticSubnetConfig, error) {
	elasticSubnetConfigPath := GetElasticSubnetConfigPath(subnetName)
	if _, err := os.Stat(elasticSubnetConfigPath); err != nil {
		return models.ElasticSubnetConfig{}, nil
	}
	jsonBytes, err := os.ReadFile(elasticSubnetConfigPath)
	if err != nil {
		return models.ElasticSubnetConfig{}, err
	}

	var esc models.ElasticSubnetConfig
	err = json.Unmarshal(jsonBytes, &esc)
	return esc, err
}

func DeleteElasticSubnetConfigs(logger logging.Logger) {
	allSubnetDirs, err := os.ReadDir(GetSubnetDir())
	if err != nil {
		ux.Print(logger, logging.Red.Wrap("error getting subnet directory"))
	}

	for _, subnetDir := range allSubnetDirs {
		if !subnetDir.IsDir() {
			continue
		}
		fmt.Printf("deleting %s \n", subnetDir.Name())
		if err := os.RemoveAll(GetSubnetNameDir(subnetDir.Name())); err != nil {
			ux.Print(logger, logging.Red.Wrap("error deleting elastic subnet config"))
		}
	}
}
