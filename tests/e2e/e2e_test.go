// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// e2e implements the e2e tests.
package e2e_test

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/client"
	"github.com/ava-labs/avalanche-network-runner/rpcpb"
	"github.com/ava-labs/avalanche-network-runner/server"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanche-network-runner/utils/constants"
	"github.com/ava-labs/avalanche-network-runner/ux"
	"github.com/ava-labs/avalanchego/api/admin"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/message"
	avago_constants "github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
)

func TestE2e(t *testing.T) {
	if os.Getenv("RUN_E2E") == "" {
		t.Skip("Environment variable RUN_E2E not set; skipping E2E tests")
	}
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "network-runner-example e2e test suites")
}

var (
	logLevel      string
	logDir        string
	gRPCEp        string
	gRPCGatewayEp string
	execPath1     string
	execPath2     string
	subnetEvmPath string

	newNodeName       = "test-add-node"
	customNodeConfigs = map[string]string{
		"node1": `{"api-admin-enabled":true}`,
		"node2": `{"api-admin-enabled":true}`,
		"node3": `{"api-admin-enabled":true}`,
		"node4": `{"api-admin-enabled":false}`,
		"node5": `{"api-admin-enabled":false}`,
		"node6": `{"api-admin-enabled":false}`,
		"node7": `{"api-admin-enabled":false}`,
	}
	numNodes = uint32(5)
)

func init() {
	flag.StringVar(
		&logLevel,
		"log-level",
		logging.Info.String(),
		"log level",
	)
	flag.StringVar(
		&logDir,
		"log-dir",
		"",
		"log directory",
	)
	flag.StringVar(
		&gRPCEp,
		"grpc-endpoint",
		"0.0.0.0:8080",
		"gRPC server endpoint",
	)
	flag.StringVar(
		&gRPCGatewayEp,
		"grpc-gateway-endpoint",
		"0.0.0.0:8081",
		"gRPC gateway endpoint",
	)
	flag.StringVar(
		&execPath1,
		"avalanchego-path-1",
		"",
		"avalanchego executable path (to upgrade from)",
	)
	flag.StringVar(
		&execPath2,
		"avalanchego-path-2",
		"",
		"avalanchego executable path (to upgrade to)",
	)
	flag.StringVar(
		&subnetEvmPath,
		"subnet-evm-path",
		"",
		"path to subnet-evm binary",
	)
}

var (
	cli client.Client
	log logging.Logger
)

var _ = ginkgo.BeforeSuite(func() {
	if logDir == "" {
		var err error
		logDir, err = os.MkdirTemp("", fmt.Sprintf("anr-e2e-logs-%d", time.Now().Unix()))
		gomega.Ω(err).Should(gomega.BeNil())
	}
	lvl, err := logging.ToLevel(logLevel)
	gomega.Ω(err).Should(gomega.BeNil())
	lcfg := logging.Config{
		DisplayLevel: lvl,
	}
	logFactory := logging.NewFactory(lcfg)
	log, err = logFactory.Make(constants.LogNameTest)
	gomega.Ω(err).Should(gomega.BeNil())

	cli, err = client.New(client.Config{
		Endpoint:    gRPCEp,
		DialTimeout: 10 * time.Second,
	}, log)
	gomega.Ω(err).Should(gomega.BeNil())
})

var _ = ginkgo.AfterSuite(func() {
	ux.Print(log, logging.Red.Wrap("shutting down cluster"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	_, err := cli.Stop(ctx)
	cancel()
	gomega.Ω(err).Should(gomega.BeNil())

	ux.Print(log, logging.Red.Wrap("shutting down client"))
	err = cli.Close()
	gomega.Ω(err).Should(gomega.BeNil())
})

var _ = ginkgo.Describe("[Start/Remove/Restart/Add/Stop]", func() {
	ginkgo.It("can create blockhains", func() {
		existingSubnetID := ""
		ginkgo.By("start with blockchain specs", func() {
			ux.Print(log, logging.Green.Wrap("sending 'start' with the valid binary path: %s"), execPath2)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			resp, err := cli.Start(ctx, execPath2,
				client.WithBlockchainSpecs([]*rpcpb.BlockchainSpec{
					{
						VmName:  "subnetevm",
						Genesis: "tests/e2e/subnet-evm-genesis.json",
					},
				}),
			)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			ux.Print(log, logging.Green.Wrap("successfully started, node-names: %s"), resp.ClusterInfo.NodeNames)
		})

		ginkgo.By("can create a blockchain with a new subnet id", func() {
			ux.Print(log, logging.Blue.Wrap("can create a blockchain in a new subnet"))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.CreateBlockchains(ctx,
				[]*rpcpb.BlockchainSpec{
					{
						VmName:  "subnetevm",
						Genesis: "tests/e2e/subnet-evm-genesis.json",
					},
				},
			)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})

		ginkgo.By("get subnet ID", func() {
			existingSubnetID = getExistingSubnetID()
		})

		ginkgo.By("can create a blockchain with an existing subnet id", func() {
			ux.Print(log, logging.Blue.Wrap("can create a blockchain in an existing subnet"))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.CreateBlockchains(ctx,
				[]*rpcpb.BlockchainSpec{
					{
						VmName:   "subnetevm",
						Genesis:  "tests/e2e/subnet-evm-genesis.json",
						SubnetId: &existingSubnetID,
					},
				},
			)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})

		ginkgo.By("can save snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.SaveSnapshot(ctx, "test")
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})

		ginkgo.By("can load snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.LoadSnapshot(ctx, "test")
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})

		// need to remove the snapshot otherwise it fails later in the 2nd part of snapshot tests
		// (testing for no snapshots)
		ginkgo.By("can remove snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.RemoveSnapshot(ctx, "test")
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})

		ginkgo.By("can create a blockchain with an existing subnet id", func() {
			ux.Print(log, logging.Blue.Wrap("can create a blockchain in an existing subnet"))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.CreateBlockchains(ctx,
				[]*rpcpb.BlockchainSpec{
					{
						VmName:   "subnetevm",
						Genesis:  "tests/e2e/subnet-evm-genesis.json",
						SubnetId: &existingSubnetID,
					},
				},
			)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})

		ginkgo.By("stop the network", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.Stop(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
	})

	ginkgo.It("can start", func() {
		ginkgo.By("start request with invalid exec path should fail", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err := cli.Start(ctx, "")
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring(utils.ErrInvalidExecPath.Error()))
		})

		ginkgo.By("start request with invalid exec path should fail", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err := cli.Start(ctx, "invalid")
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring(utils.ErrNotExists.Error()))
		})

		ginkgo.By("start request with invalid custom VM path should fail", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err := cli.Start(ctx, execPath1,
				client.WithPluginDir(os.TempDir()),
				client.WithBlockchainSpecs([]*rpcpb.BlockchainSpec{
					{
						VmName: "invalid",
					},
				}),
			)
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring(utils.ErrNotExistsPlugin.Error()))
		})

		ginkgo.By("start request with invalid custom VM name format should fail", func() {
			f, err := os.CreateTemp(os.TempDir(), strings.Repeat("a", 33))
			gomega.Ω(err).Should(gomega.BeNil())
			filePath := f.Name()
			gomega.Ω(f.Close()).Should(gomega.BeNil())

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err = cli.Start(ctx, execPath1,
				client.WithPluginDir(filepath.Dir(filePath)),
				client.WithBlockchainSpecs([]*rpcpb.BlockchainSpec{
					{
						VmName: filepath.Base(filePath),
					},
				}),
			)
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring(server.ErrInvalidVMName.Error()))

			os.RemoveAll(filePath)
		})

		ginkgo.By("start request with invalid custom VM genesis path should fail", func() {
			vmID, err := utils.VMID("hello")
			gomega.Ω(err).Should(gomega.BeNil())
			filePath := filepath.Join(os.TempDir(), vmID.String())
			gomega.Ω(os.WriteFile(filePath, []byte{0}, fs.ModePerm)).Should(gomega.BeNil())

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err = cli.Start(ctx, execPath1,
				client.WithPluginDir(filepath.Dir(filePath)),
				client.WithBlockchainSpecs([]*rpcpb.BlockchainSpec{
					{
						VmName:  "hello",
						Genesis: "invalid",
					},
				}),
			)
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring(utils.ErrNotExistsPluginGenesis.Error()))

			os.RemoveAll(filePath)
		})

		ginkgo.By("calling start API with the valid binary path", func() {
			ux.Print(log, logging.Green.Wrap("sending 'start' with the valid binary path: %s"), execPath1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			resp, err := cli.Start(ctx, execPath1)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			ux.Print(log, logging.Green.Wrap("successfully started, node-names: %s"), resp.ClusterInfo.NodeNames)
		})
	})

	ginkgo.It("can wait for health", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		_, err := cli.Health(ctx)
		cancel()
		gomega.Ω(err).Should(gomega.BeNil())
	})

	ginkgo.It("can get URIs", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		uris, err := cli.URIs(ctx)
		cancel()
		gomega.Ω(err).Should(gomega.BeNil())
		ux.Print(log, logging.Blue.Wrap("URIs: %s"), uris)
	})

	ginkgo.It("can fetch status", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, err := cli.Status(ctx)
		cancel()
		gomega.Ω(err).Should(gomega.BeNil())
	})

	ginkgo.It("can poll status", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		ch, err := cli.StreamStatus(ctx, 5*time.Second)
		gomega.Ω(err).Should(gomega.BeNil())
		for info := range ch {
			ux.Print(log, logging.Green.Wrap("fetched info, node-names: %s"), info.NodeNames)
			if info.Healthy {
				break
			}
		}
	})

	time.Sleep(10 * time.Second)
	ginkgo.It("can remove", func() {
		ginkgo.By("calling remove API with the first binary", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			resp, err := cli.RemoveNode(ctx, "node5")
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			ux.Print(log, logging.Green.Wrap("successfully removed, node-names: %s"), resp.ClusterInfo.NodeNames)
		})
	})

	time.Sleep(10 * time.Second)
	ginkgo.It("can restart", func() {
		ginkgo.By("calling restart API with the second binary", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			resp, err := cli.RestartNode(ctx, "node4", client.WithExecPath(execPath2))
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			ux.Print(log, logging.Green.Wrap("successfully restarted, node-names: %s"), resp.ClusterInfo.NodeNames)
		})
	})

	time.Sleep(10 * time.Second)
	ginkgo.It("can attach a peer", func() {
		ginkgo.By("calling attach peer API", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			resp, err := cli.AttachPeer(ctx, "node1")
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())

			v, ok := resp.ClusterInfo.AttachedPeerInfos["node1"]
			gomega.Ω(ok).Should(gomega.BeTrue())
			ux.Print(log, logging.Green.Wrap("successfully attached peer, peers: %+v"), v.Peers)

			mc, err := message.NewCreator(
				prometheus.NewRegistry(),
				"",
				true,
				10*time.Second,
			)
			gomega.Ω(err).Should(gomega.BeNil())

			containerIDs := []ids.ID{
				ids.GenerateTestID(),
				ids.GenerateTestID(),
				ids.GenerateTestID(),
			}
			requestID := uint32(42)
			chainID := avago_constants.PlatformChainID
			msg, err := mc.Chits(chainID, requestID, containerIDs)
			gomega.Ω(err).Should(gomega.BeNil())

			ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
			sresp, err := cli.SendOutboundMessage(ctx, "node1", v.Peers[0].Id, uint32(msg.Op()), msg.Bytes())
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			gomega.Ω(sresp.Sent).Should(gomega.BeTrue())
		})
	})

	time.Sleep(10 * time.Second)
	ginkgo.It("can add a node", func() {
		ginkgo.By("calling AddNode", func() {
			ux.Print(log, logging.Green.Wrap("calling 'add-node' with the valid binary path: %s"), execPath1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			resp, err := cli.AddNode(ctx, newNodeName, execPath1)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			ux.Print(log, logging.Green.Wrap("successfully started, node-names: %s"), resp.ClusterInfo.NodeNames)
		})

		ginkgo.By("calling AddNode with existing node name, should fail", func() {
			ux.Print(log, logging.Green.Wrap("calling 'add-node' with the valid binary path: %s"), execPath1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			resp, err := cli.AddNode(ctx, newNodeName, execPath1)
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring("repeated node name"))
			gomega.Ω(resp).Should(gomega.BeNil())
			ux.Print(log, logging.Green.Wrap("'add-node' failed as expected"))
		})
	})

	ginkgo.It("can start with custom config", func() {
		ginkgo.By("stopping network first", func() {
			ux.Print(log, logging.Red.Wrap("shutting down cluster"))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.Stop(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())

			ux.Print(log, logging.Red.Wrap("shutting down client"))
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("calling start API with custom config", func() {
			ux.Print(log, logging.Green.Wrap("sending 'start' with the valid binary path: %s"), execPath1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			opts := []client.OpOption{
				client.WithNumNodes(numNodes),
				client.WithCustomNodeConfigs(customNodeConfigs),
			}
			resp, err := cli.Start(ctx, execPath1, opts...)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			ux.Print(log, logging.Green.Wrap("successfully started, node-names: %s"), resp.ClusterInfo.NodeNames)
		})
		ginkgo.By("can wait for health", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.Health(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("overrides num-nodes", func() {
			ux.Print(log, logging.Green.Wrap("checking that given num-nodes %d have been overriden by custom configs: %d"), numNodes, len(customNodeConfigs))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			uris, err := cli.URIs(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			gomega.Ω(uris).Should(gomega.HaveLen(len(customNodeConfigs)))
			ux.Print(log, logging.Green.Wrap("expected number of nodes up: %d"), len(customNodeConfigs))

			ux.Print(log, logging.Green.Wrap("checking correct admin APIs are enabled resp. disabled"))
			// we have 7 nodes, 3 have the admin API enabled, the other 4 disabled
			// therefore we expect exactly 4 calls to fail and exactly 3 to succeed.
			ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
			errCnt := 0
			for i := 0; i < len(uris); i++ {
				cli := admin.NewClient(uris[i])
				err := cli.LockProfile(ctx)
				if err != nil {
					errCnt++
				}
			}
			cancel()
			gomega.Ω(errCnt).Should(gomega.Equal(4))
		})
	})

	ginkgo.It("start with default network, for subsecuent steps", func() {
		ginkgo.By("stopping network first", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.Stop(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("starting", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err := cli.Start(ctx, execPath1)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
	})

	ginkgo.It("subnet creation", func() {
		ginkgo.By("check subnets are 0", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			status, err := cli.Status(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			numSubnets := len(status.ClusterInfo.Subnets)
			gomega.Ω(numSubnets).Should(gomega.Equal(0))
		})
		ginkgo.By("add 1 subnet", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.CreateSubnets(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("wait for the network to be healthy", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.Health(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("check subnets are 1", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			status, err := cli.Status(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			numSubnets := len(status.ClusterInfo.Subnets)
			gomega.Ω(numSubnets).Should(gomega.Equal(1))
		})
	})

	ginkgo.It("snapshots + blockchain creation", func() {
		var originalUris []string
		var originalSubnets []string
		ginkgo.By("get original URIs", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			var err error
			originalUris, err = cli.URIs(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			gomega.Ω(len(originalUris)).Should(gomega.Equal(5))
		})
		ginkgo.By("get original subnets", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			status, err := cli.Status(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			numSubnets := len(status.ClusterInfo.Subnets)
			gomega.Ω(numSubnets).Should(gomega.Equal(1))
			originalSubnets = status.ClusterInfo.Subnets
		})
		ginkgo.By("check there are no snapshots", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			snapshotNames, err := cli.GetSnapshotNames(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			gomega.Ω(snapshotNames).Should(gomega.Equal([]string(nil)))
		})
		ginkgo.By("can save snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.SaveSnapshot(ctx, "pepe")
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("load fail for unknown snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.LoadSnapshot(ctx, "papa")
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring("snapshot not found"))
		})
		ginkgo.By("can load snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.LoadSnapshot(ctx, "pepe")
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("wait for the network to be healthy", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.Health(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("check URIs", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			var err error
			uris, err := cli.URIs(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			gomega.Ω(uris).Should(gomega.Equal(originalUris))
		})
		ginkgo.By("check subnets", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			status, err := cli.Status(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			gomega.Ω(status.ClusterInfo.Subnets).Should(gomega.Equal(originalSubnets))
		})
		ginkgo.By("save fail for already saved snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.SaveSnapshot(ctx, "pepe")
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring("snapshot \"pepe\" already exists"))
		})
		ginkgo.By("check there is a snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			snapshotNames, err := cli.GetSnapshotNames(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			gomega.Ω(snapshotNames).Should(gomega.Equal([]string{"pepe"}))
		})
		ginkgo.By("can remove snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.RemoveSnapshot(ctx, "pepe")
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("check there are no snapshots", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			snapshotNames, err := cli.GetSnapshotNames(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			gomega.Ω(snapshotNames).Should(gomega.Equal([]string(nil)))
		})
		ginkgo.By("remove fail for unknown snapshot", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.RemoveSnapshot(ctx, "pepe")
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring("snapshot not found"))
		})
	})
})

func getExistingSubnetID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	status, err := cli.Status(ctx)
	cancel()
	gomega.Ω(err).Should(gomega.BeNil())
	existingSubnetID := status.ClusterInfo.GetSubnets()[0]
	gomega.Ω(existingSubnetID).Should(gomega.Not(gomega.BeNil()))
	return existingSubnetID
}
