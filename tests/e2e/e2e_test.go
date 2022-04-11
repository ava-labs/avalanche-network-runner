// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

// e2e implements the e2e tests.
package e2e_test

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/ava-labs/avalanche-network-runner/client"
	"github.com/ava-labs/avalanche-network-runner/pkg/color"
	"github.com/ava-labs/avalanche-network-runner/pkg/logutil"
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

var _ = ginkgo.Describe("[Start/Remove/Restart/Stop]", func() {
	ginkgo.It("can start", func() {
		ginkgo.By("calling start API with the first binary", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			resp, err := cli.Start(ctx, execPath1)
			cancel()
			gomega.Ω(err).Should(gomega.BeNil())
			color.Outf("{{green}}successfully started:{{/}} %+v\n", resp.ClusterInfo.NodeNames)
		})
	})

	ginkgo.It("can wait for health", func() {
		// start is async, so wait some time for cluster health
		time.Sleep(2 * time.Minute)

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
			resp, err := cli.RestartNode(ctx, "node4", execPath2)
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
})
