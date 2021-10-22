package localoperator

const (
	AVALANCHEGO = iota
	BYZANTINE   = iota
)

type NodeConfig struct {
	BinKind     int
	NodeID      string
	PrivateKey  []byte
	Cert        []byte
	ConfigFlags []byte
}

type NetworkConfig struct {
	Genesis         []byte
	CChainConfig    []byte
	CoreConfigFlags []byte
	NodeConfigs     []NodeConfig
}
