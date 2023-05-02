package pkg

import (
	"encoding/json"
	"fmt"
	"github.com/ava-labs/avalanche-network-runner/pkg/models"
	"os"
	"os/user"
	"path/filepath"
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
	jsonBytes, err := os.ReadFile(elasticSubnetConfigPath)
	if err != nil {
		return models.ElasticSubnetConfig{}, err
	}

	var esc models.ElasticSubnetConfig
	err = json.Unmarshal(jsonBytes, &esc)
	return esc, err
}
