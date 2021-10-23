package localnetworkrunner

type NetworkContext struct {
	network *Network
}

func NewNetworkContext(network *Network) *NetworkContext {
	return &NetworkContext{network}
}

func (nc *NetworkContext) Fatal(err error) {
	nc.network.Stop()
	panic(err)
}
