// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// e2e implements the e2e tests.
package e2e_test

import (
	"context"
	"flag"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/client"
	"github.com/ava-labs/avalanche-network-runner/pkg/color"
	"github.com/ava-labs/avalanche-network-runner/pkg/logutil"
	"github.com/ava-labs/avalanche-network-runner/server"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/api/admin"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/message"
	"github.com/ava-labs/avalanchego/utils/constants"
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
	gRPCEp        string
	gRPCGatewayEp string
	execPath1     string
	execPath2     string

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
		logutil.DefaultLogLevel,
		"log level",
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
}

var cli client.Client

var _ = ginkgo.BeforeSuite(func() {
	var err error
	cli, err = client.New(client.Config{
		LogLevel:    logLevel,
		Endpoint:    gRPCEp,
		DialTimeout: 10 * time.Second,
	})
	gomega.Ω(err).Should(gomega.BeNil())
})

var _ = ginkgo.AfterSuite(func() {
	color.Outf("{{red}}shutting down cluster{{/}}\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	_, err := cli.Stop(ctx)
	cancel()
	gomega.Ω(err).Should(gomega.BeNil())

	color.Outf("{{red}}shutting down client{{/}}\n")
	err = cli.Close()
	gomega.Ω(err).Should(gomega.BeNil())
})

var _ = ginkgo.Describe("[Start/Remove/Restart/Add/Stop]", func() {
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
				client.WithCustomVMs(map[string]string{"invalid": "{0}"}),
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
				client.WithCustomVMs(map[string]string{filepath.Base(filePath): "{0}"}),
			)
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring(server.ErrInvalidVMName.Error()))

			os.RemoveAll(filePath)
		})

		ginkgo.By("start request with missing plugin dir should fail", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err := cli.Start(ctx, execPath1,
				client.WithCustomVMs(map[string]string{"test": "{0}"}),
			)
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring(server.ErrPluginDirEmptyButCustomVMsNotEmpty.Error()))
		})

		ginkgo.By("start request with missing custom VMs should fail", func() {
			f, err := os.CreateTemp(os.TempDir(), strings.Repeat("a", 33))
			gomega.Ω(err).Should(gomega.BeNil())
			filePath := f.Name()
			gomega.Ω(f.Close()).Should(gomega.BeNil())

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err = cli.Start(ctx, execPath1,
				client.WithPluginDir(filepath.Dir(filePath)),
			)
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring(server.ErrPluginDirNonEmptyButCustomVMsEmpty.Error()))

			os.RemoveAll(filePath)
		})

		ginkgo.By("start request with invalid custom VM genesis path should fail", func() {
			vmID, err := utils.VMID("hello")
			gomega.Ω(err).Should(gomega.BeNil())
			filePath := filepath.Join(os.TempDir(), vmID.String())
			gomega.Ω(ioutil.WriteFile(filePath, []byte{0}, fs.ModePerm)).Should(gomega.BeNil())

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, err = cli.Start(ctx, execPath1,
				client.WithPluginDir(filepath.Dir(filePath)),
				client.WithCustomVMs(map[string]string{"hello": "invalid"}),
			)
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring(utils.ErrNotExistsPluginGenesis.Error()))

			os.RemoveAll(filePath)
		})

		ginkgo.By("calling start API with the valid binary path", func() {
			color.Outf("{{green}}sending 'start' with the valid binary path:{{/}} %q\n", execPath1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			resp, err := cli.Start(ctx, execPath1)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			color.Outf("{{green}}successfully started:{{/}} %+v\n", resp.ClusterInfo.NodeNames)
		})
	})

	ginkgo.It("can wait for health", func() {
		// start is async, so wait some time for cluster health
		// TODO: Don't sleep. Use polling or other mechanism. Apply to all Sleeps in the test.
		time.Sleep(30 * time.Second)

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
		color.Outf("{{blue}}URIs:{{/}} %q\n", uris)
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
			color.Outf("{{green}}fetched info:{{/}} %+v\n", info.NodeNames)
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
			color.Outf("{{green}}successfully removed:{{/}} %+v\n", resp.ClusterInfo.NodeNames)
		})
	})

	time.Sleep(10 * time.Second)
	ginkgo.It("can restart", func() {
		ginkgo.By("calling restart API with the second binary", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			resp, err := cli.RestartNode(ctx, "node4", client.WithExecPath(execPath2))
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			color.Outf("{{green}}successfully restarted:{{/}} %+v\n", resp.ClusterInfo.NodeNames)
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
			color.Outf("{{green}}successfully attached peer:{{/}} %+v\n", v.Peers)

			mc, err := message.NewCreator(
				prometheus.NewRegistry(),
				true,
				"",
				10*time.Second,
			)
			gomega.Ω(err).Should(gomega.BeNil())

			containerIDs := []ids.ID{
				ids.GenerateTestID(),
				ids.GenerateTestID(),
				ids.GenerateTestID(),
			}
			requestID := uint32(42)
			chainID := constants.PlatformChainID
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
			color.Outf("{{green}}calling 'add-node' with the valid binary path:{{/}} %q\n", execPath1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			resp, err := cli.AddNode(ctx, newNodeName, execPath1)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			color.Outf("{{green}}successfully started:{{/}} %+v\n", resp.ClusterInfo.NodeNames)
		})

		ginkgo.By("calling AddNode with existing node name, should fail", func() {
			color.Outf("{{green}}calling 'add-node' with the valid binary path:{{/}} %q\n", execPath1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			resp, err := cli.AddNode(ctx, newNodeName, execPath1)
			cancel()
			gomega.Ω(err.Error()).Should(gomega.ContainSubstring("already exists"))
			gomega.Ω(resp).Should(gomega.BeNil())
			color.Outf("{{green}}add-node failed as expected")
		})
	})

	ginkgo.It("can start with custom config", func() {
		ginkgo.By("stopping network first", func() {
			color.Outf("{{red}}shutting down cluster{{/}}\n")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.Stop(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())

			color.Outf("{{red}}shutting down client{{/}}\n")
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("calling start API with custom config", func() {
			color.Outf("{{green}}sending 'start' with the valid binary path:{{/}} %q\n", execPath1)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			opts := []client.OpOption{
				client.WithNumNodes(numNodes),
				client.WithCustomNodeConfigs(customNodeConfigs),
			}
			resp, err := cli.Start(ctx, execPath1, opts...)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			color.Outf("{{green}}successfully started:{{/}} %+v\n", resp.ClusterInfo.NodeNames)
		})
		ginkgo.By("can wait for health", func() {
			// start is async, so wait some time for cluster health
			time.Sleep(30 * time.Second)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := cli.Health(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
		})
		ginkgo.By("overrides num-nodes", func() {
			color.Outf("{{green}}checking that given num-nodes %d have been overriden by custom configs with %d:\n", numNodes, len(customNodeConfigs))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			uris, err := cli.URIs(ctx)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			gomega.Ω(uris).Should(gomega.HaveLen(len(customNodeConfigs)))
			color.Outf("{{green}}expected number of nodes up:{{/}} %q\n", len(customNodeConfigs))

			color.Outf("{{green}}checking correct admin APIs are enabled resp. disabled")
			// we have 7 nodes, 3 have the admin API enabled, the other 4 disabled
			// therefore we expect exactly 4 calls to fail and exactly 3 to succeed.
			ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
			errCnt := 0
			for i := 0; i < len(uris); i++ {
				cli := admin.NewClient(uris[i])
				_, err := cli.LockProfile(ctx)
				if err != nil {
					errCnt++
				}
			}
			cancel()
			gomega.Ω(errCnt).Should(gomega.Equal(4))
		})
	})
})
