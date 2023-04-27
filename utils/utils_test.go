package utils

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

var genesis = []byte(
	`{
		"networkID": 1337,
		"allocations": [
		  {
			"ethAddr": "0xb3d82b1367d362de99ab59a658165aff520cbd4d",
			"avaxAddr": "X-custom1g65uqn6t77p656w64023nh8nd9updzmxwd59gh",
			"initialAmount": 0,
			"unlockSchedule": [
			  {
				"amount": 10000000000000000,
				"locktime": 1633824000
			  }
			]
		  }
		],
		"startTime": 1630987200,
		"initialStakeDuration": 31536000,
		"initialStakeDurationOffset": 5400,
		"initialStakedFunds": [
		  "X-custom1g65uqn6t77p656w64023nh8nd9updzmxwd59gh"
		],
		"initialStakers": [
		  {
			"nodeID": "NodeID-7Xhw2mDxuDS44j42TCB6U5579esbSt3Lg",
			"rewardAddress": "X-custom18jma8ppw3nhx5r4ap8clazz0dps7rv5u9xde7p",
			"delegationFee": 1000000
		  }
		],
		"cChainGenesis": "{\"config\":{\"chainId\":43112,\"homesteadBlock\":0,\"daoForkBlock\":0,\"daoForkSupport\":true,\"eip150Block\":0,\"eip150Hash\":\"0x2086799aeebeae135c246c65021c82b4e15a2c451340993aacfd2751886514f0\",\"eip155Block\":0,\"eip158Block\":0,\"byzantiumBlock\":0,\"constantinopleBlock\":0,\"petersburgBlock\":0,\"istanbulBlock\":0,\"muirGlacierBlock\":0,\"apricotPhase1BlockTimestamp\":0,\"apricotPhase2BlockTimestamp\":0},\"nonce\":\"0x0\",\"timestamp\":\"0x0\",\"extraData\":\"0x00\",\"gasLimit\":\"0x5f5e100\",\"difficulty\":\"0x0\",\"mixHash\":\"0x0000000000000000000000000000000000000000000000000000000000000000\",\"coinbase\":\"0x0000000000000000000000000000000000000000\",\"alloc\":{\"8db97C7cEcE249c2b98bDC0226Cc4C2A57BF52FC\":{\"balance\":\"0x295BE96E64066972000000\"}},\"number\":\"0x0\",\"gasUsed\":\"0x0\",\"parentHash\":\"0x0000000000000000000000000000000000000000000000000000000000000000\"}",
		"message": "{{ fun_quote }}"
	  }`,
)

// TestExtractNetworkID tests the internal getNetworkID method which
// extracts the NetworkID from the genesis file
func TestExtractNetworkID(t *testing.T) {
	netID, err := NetworkIDFromGenesis(genesis)
	require.NoError(t, err)
	require.EqualValues(t, netID, 1337)
}

func TestCheckExecPath(t *testing.T) {
	execF, err := os.CreateTemp(os.TempDir(), "test-check-exec")
	require.NoError(t, err)
	execPath := execF.Name()
	require.NoError(t, execF.Close())

	t.Cleanup(func() {
		err = os.RemoveAll(execPath)
		require.NoError(t, err)
	})

	tt := []struct {
		execPath    string
		expectedErr error
	}{
		{
			execPath:    execPath,
			expectedErr: nil,
		},
		{
			execPath:    "",
			expectedErr: ErrInvalidExecPath,
		},
		{
			execPath:    "invalid",
			expectedErr: ErrNotExists,
		},
	}
	for i, tv := range tt {
		err := CheckExecPath(tv.execPath)
		require.Equal(t, tv.expectedErr, err, fmt.Sprintf("[%d] unexpected error", i))
	}
}

func TestCheckPluginPaths(t *testing.T) {
	pluginF, err := os.CreateTemp(os.TempDir(), "test-check-exec-plugin")
	require.NoError(t, err)
	pluginPath := pluginF.Name()
	require.NoError(t, pluginF.Close())

	genesisF, err := os.CreateTemp(os.TempDir(), "test-check-genesis-plugin")
	require.NoError(t, err)
	genesisPath := genesisF.Name()
	require.NoError(t, genesisF.Close())

	t.Cleanup(func() {
		err = os.RemoveAll(pluginPath)
		require.NoError(t, err)
		err = os.RemoveAll(genesisPath)
		require.NoError(t, err)
	})

	tt := []struct {
		pluginPath  string
		genesisPath string
		expectedErr error
	}{
		{
			pluginPath:  pluginPath,
			genesisPath: genesisPath,
			expectedErr: nil,
		},
		{
			pluginPath:  "invalid",
			genesisPath: "",
			expectedErr: ErrNotExistsPlugin,
		},
	}
	for i, tv := range tt {
		err := CheckPluginPath(tv.pluginPath)
		require.Equal(t, tv.expectedErr, err, fmt.Sprintf("[%d] unexpected error", i))
	}
}
