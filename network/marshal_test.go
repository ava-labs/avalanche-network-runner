package network

import (
	"encoding/json"
	"testing"
)

func TestMarshalling(t *testing.T) {
	jsonNetcfg := "{\"implSpecCfg\":\"\",\"genesis\":\"in the beginning there was a token\",\"nodeConfigs\":[{\"implSpecCfg\":{\"binaryPath\":\"/tmp/some/file/path\"},\"name\":\"node0\",\"isBeacon\":true,\"stakingKey\":\"key123\",\"stakingCert\":\"cert123\",\"confFile\":\"config-file-blablabla1\",\"cchainConfFile\":\"cchain-config-file-blablabla1\"},{\"implSpecCfg\":{\"apiVersion\":\"0.1.99\",\"identifier\":\"k8s-node-1\",\"image\":\"therepo/theimage\",\"kind\":\"imaginary\",\"namespace\":\"outer-space\",\"tag\":\"omega\"},\"name\":\"node1\",\"isBeacon\":false,\"stakingKey\":\"key456\",\"stakingCert\":\"cert456\",\"confFile\":\"config-file-blablabla2\",\"cchainConfFile\":\"cchain-config-file-blablabla2\"},{\"implSpecCfg\":\"\",\"name\":\"node2\",\"isBeacon\":false,\"stakingKey\":\"key789\",\"stakingCert\":\"cert789\",\"confFile\":\"config-file-blablabla3\",\"cchainConfFile\":\"cchain-config-file-blablabla3\"}],\"logLevel\":\"DEBUG\",\"name\":\"abcxyz\"}"

	var netcfg Config
	if err := json.Unmarshal([]byte(jsonNetcfg), &netcfg); err != nil {
		t.Fatal(err)
	}

	var cfgBytes []byte
	cfgBytes, err := json.Marshal(netcfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(cfgBytes) != jsonNetcfg {
		t.Fatalf("Expected %s, but got %s", jsonNetcfg, string(cfgBytes))
	}
}
