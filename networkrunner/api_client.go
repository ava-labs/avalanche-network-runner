package networkrunner

import (
	"github.com/ava-labs/avalanchego/api/admin"
	"github.com/ava-labs/avalanchego/api/health"
	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/api/ipcs"
	"github.com/ava-labs/avalanchego/api/keystore"
	"github.com/ava-labs/avalanchego/indexer"
	"github.com/ava-labs/avalanchego/vms/avm"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/coreth/plugin/evm"
)

// Issues API calls to a node
type APIClient interface {
    PChainAPI() *platformvm.Client
    XChainAPI() *avm.Client
    XChainWalletAPI() *avm.WalletClient
    CChainAPI() *evm.Client
    CChainEthAPI() *EthClient
    InfoAPI() *info.Client
    HealthAPI() *health.Client
    IpcsAPI() *ipcs.Client
    KeystoreAPI() *keystore.Client
    AdminAPI() *admin.Client
    PChainIndexAPI() *indexer.Client
    CChainIndexAPI() *indexer.Client
	// TODO add methods
}
