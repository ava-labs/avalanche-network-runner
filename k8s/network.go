package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"golang.org/x/sync/errgroup"

	k8sapi "github.com/ava-labs/avalanchego-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	k8scli "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// How long we'll wait for a kubernetes pod to become reachable
	nodeReachableTimeout = 2 * time.Minute
	// Time between checks to see if a node is reachable
	nodeReachableRetryFreq = 3 * time.Second
	// Prefix the avalanchego-operator uses to pass params to avalanchego nodes
	envVarPrefix = "AVAGO_"
)

var _ network.Network = (*networkImpl)(nil)

// Returns a new K8s client.
// We define this type so we can mock the K8s client.
type newClientFunc func() (k8scli.Client, error)

// networkParams encapsulate params to create a network
type networkParams struct {
	conf          network.Config
	log           logging.Logger
	newClientFunc newClientFunc
	dnsChecker    dnsReachableChecker
	apiClientFunc api.NewAPIClientF
}

// networkImpl is the kubernetes data type representing a kubernetes network adapter.
// It implements the network.Network interface.
type networkImpl struct {
	log    logging.Logger
	config network.Config
	// the kubernetes client
	k8scli k8scli.Client
	// Node name --> The node
	nodes map[string]*Node
	// URI of the beacon node
	// TODO allow multiple beacons
	beaconURL string
	// Closed when network is done shutting down
	closedOnStopCh chan struct{}
	// Checks if a node is reachable via DNS
	dnsChecker dnsReachableChecker
	// Create the K8s API client
	apiClientFunc api.NewAPIClientF
}

func newK8sClient() (k8scli.Client, error) {
	// init k8s client
	scheme := runtime.NewScheme()
	if err := k8sapi.AddToScheme(scheme); err != nil {
		return nil, err
	}
	kubeconfig := ctrl.GetConfigOrDie()
	return k8scli.New(kubeconfig, k8scli.Options{Scheme: scheme})
}

func newNetwork(params networkParams) (network.Network, error) {
	kubeClient, err := params.newClientFunc()
	if err != nil {
		return nil, err
	}
	params.log.Info("K8s client initialized")
	beacons, nonBeacons, err := createDeploymentFromConfig(params.conf.Genesis, params.conf.NodeConfigs)
	if err != nil {
		return nil, err
	}
	if len(beacons) == 0 {
		return nil, errors.New("NodeConfigs don't describe any beacon nodes")
	}
	net := &networkImpl{
		config:         params.conf,
		k8scli:         kubeClient,
		closedOnStopCh: make(chan struct{}),
		log:            params.log,
		nodes:          make(map[string]*Node, len(params.conf.NodeConfigs)),
		dnsChecker:     params.dnsChecker,
		apiClientFunc:  params.apiClientFunc,
	}
	net.log.Debug("launching beacon nodes...")
	// Start the beacon nodes
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if err := net.k8scli.DeleteAllOf(ctx, &k8sapi.Avalanchego{}, &k8scli.DeleteAllOfOptions{}); err != nil {
			net.log.Error("Error clearing all of the k8s objects: %s", err)
		}
	}()
	if err := net.launchNodes(beacons); err != nil {
		return nil, fmt.Errorf("error launching beacons: %w", err)
	}
	// Tell future nodes the IP of the beacon node
	// TODO add support for multiple beacons
	net.beaconURL = beacons[0].Status.NetworkMembersURI[0]
	if net.beaconURL == "" {
		return nil, errors.New("Bootstrap URI is set to empty")
	}
	net.log.Info("Beacon node started")
	// Start the non-beacon nodes
	if err := net.launchNodes(nonBeacons); err != nil {
		return nil, fmt.Errorf("Error launching non-beacons: %s", err)
	}
	net.log.Info("All nodes started")
	// Build a mapping from k8s URIs to names/ids
	if err := net.buildNodeMapping(append(beacons, nonBeacons...)); err != nil {
		return nil, err
	}

	net.log.Info("network: %s", net)
	return net, nil
}

// NewNetwork returns a new network whose initial state is specified in the config
func NewNetwork(conf network.Config, log logging.Logger) (network.Network, error) {
	return newNetwork(networkParams{
		conf:          conf,
		log:           log,
		newClientFunc: newK8sClient,
		dnsChecker:    &defaultDNSReachableChecker{},
		apiClientFunc: api.NewAPIClient,
	})
}

// See network.Network
func (a *networkImpl) GetNodesNames() ([]string, error) {
	nodes := make([]string, len(a.nodes))
	i := 0
	for _, n := range a.nodes {
		nodes[i] = n.name
		i++
	}
	return nodes, nil
}

// See network.Network
func (a *networkImpl) Healthy(ctx context.Context) chan error {
	errCh := make(chan error, 1)

	go func() {
		errGr, ctx := errgroup.WithContext(context.Background())
		for _, node := range a.nodes {
			node := node
			errGr.Go(func() error {
				// Every constants.HealthCheckInterval, query node for health status.
				// Do this until ctx timeout
				for {
					select {
					case <-a.closedOnStopCh:
						return network.ErrStopped
					case <-ctx.Done():
						return fmt.Errorf("node %q failed to become healthy within timeout", node.GetName())
					case <-time.After(healthCheckFreq):
					}
					health, err := node.client.HealthAPI().Health()
					if err == nil && health.Healthy {
						a.log.Info("node %q became healthy", node.GetName())
						return nil
					}
				}
			})
		}
		// Wait until all nodes are ready or timeout
		if err := errGr.Wait(); err != nil {
			errCh <- err
		}
		close(errCh)
	}()
	return errCh
}

// See network.Network
func (a *networkImpl) Stop(ctx context.Context) error {
	failCount := 0
	for s, n := range a.nodes {
		a.log.Debug("Shutting down node %s...", s)
		if err := a.k8scli.Delete(ctx, n.k8sObj); err != nil {
			a.log.Error("error while stopping node %s: %s", n.name, err)
			failCount++
		}
	}
	close(a.closedOnStopCh)
	if failCount > 0 {
		return fmt.Errorf("%d nodes failed shutting down", failCount)
	}
	a.log.Info("Network stopped")
	return nil
}

// AddNode starts a new node with the given config
func (a *networkImpl) AddNode(cfg node.Config) (node.Node, error) {
	node, err := buildK8sObj(a.config.Genesis, cfg)
	if err != nil {
		return nil, err
	}
	a.log.Debug("Adding new node %s to network...", cfg.Name)
	if err := a.k8scli.Create(context.Background(), node); err != nil {
		return nil, err
	}

	a.log.Debug("Launching new node %s to network...", cfg.Name)
	if err := a.launchNodes([]*k8sapi.Avalanchego{node}); err != nil {
		return nil, err
	}

	uri := node.Status.NetworkMembersURI[0]
	cli := a.apiClientFunc(uri, defaultAPIPort, apiTimeout)
	nodeIDStr, err := cli.InfoAPI().GetNodeID()
	if err != nil {
		return nil, err
	}
	a.log.Debug("Successful. NodeID for this node is %s", nodeIDStr)
	nodeID, err := ids.ShortFromPrefixedString(nodeIDStr, constants.NodeIDPrefix)
	if err != nil {
		return nil, fmt.Errorf("could not convert node id from string: %s", err)
	}
	n := &Node{
		uri:    uri,
		client: cli,
		name:   node.Spec.DeploymentName,
		nodeID: nodeID,
		k8sObj: node,
	}
	a.nodes[n.name] = n
	return n, nil
}

// See network.Network
func (a *networkImpl) RemoveNode(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), removeTimeout)
	defer cancel()

	if p, ok := a.nodes[name]; ok {
		if err := a.k8scli.Delete(ctx, p.k8sObj); err != nil {
			return err
		}
		a.log.Info("Removed node %s", p)
		delete(a.nodes, name)
		return nil
	}
	return fmt.Errorf("node %q not found", name)
}

// GetAllNodes returns all nodes
func (a *networkImpl) GetAllNodes() []node.Node {
	nodes := make([]node.Node, len(a.nodes))
	i := 0
	for _, n := range a.nodes {
		nodes[i] = n
		i++
	}
	return nodes
}

// See network.Network
func (a *networkImpl) GetNode(name string) (node.Node, error) {
	if n, ok := a.nodes[name]; ok {
		return n, nil
	}
	return nil, fmt.Errorf("node %q not found", name)
}

// Create Kubernetes pods running AvalancheGo and wait until
// they are reachable
func (a *networkImpl) launchNodes(nodes []*k8sapi.Avalanchego) error {
	ctx, cancel := context.WithTimeout(context.Background(), nodeReachableTimeout)
	defer cancel()

	errGr, ctx := errgroup.WithContext(ctx)
	for _, n := range nodes {
		if a.beaconURL != "" {
			n.Spec.BootstrapperURL = a.beaconURL
		}
		// Create a Kubernetes pod for this node
		if err := a.k8scli.Create(context.Background(), n); err != nil {
			return err
		}

		a.log.Debug("Waiting for pod to be created...")
		for len(n.Status.NetworkMembersURI) != 1 {
			err := a.k8scli.Get(context.Background(), types.NamespacedName{
				Name:      n.Name,
				Namespace: n.Namespace,
			}, n)
			if err != nil {
				return err
			}
			select {
			case <-time.After(nodeReachableCheckFreq):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		a.log.Debug("pod created. Waiting to be reachable...")
		// Try connecting to nodes until the DNS resolves,
		// otherwise we have to sleep indiscriminately, we can't just use the API right away:
		// the kubernetes cluster has already created the pod(s) but not the DNS names,
		// so using the API Client too early results in an error.
		node := n
		errGr.Go(func() error {
			fmturi := fmt.Sprintf("http://%s:%d", node.Status.NetworkMembersURI[0], defaultAPIPort)
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					a.log.Debug("checking if %s is reachable...", fmturi)
					// TODO is there a better way to wait until the node is reachable?
					if err := a.dnsChecker.Reachable(fmturi); err == nil {
						a.log.Debug("%s has become reachable", fmturi)
						return nil
					}
					// Wait before checking again
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(nodeReachableRetryFreq):
					}
				}
			}
		})
	}
	// Wait until all nodes are ready or timeout
	if err := errGr.Wait(); err != nil {
		return err
	}
	return nil
}

// Updates [a.nodes] to include [nodes]
// It also establishes connection via the API client.
func (a *networkImpl) buildNodeMapping(nodes []*k8sapi.Avalanchego) error {
	for _, n := range nodes {
		uri := n.Status.NetworkMembersURI[0]
		a.log.Debug("creating network node and client for %s", uri)
		cli := a.apiClientFunc(uri, defaultAPIPort, apiTimeout)
		nodeIDStr, err := cli.InfoAPI().GetNodeID()
		if err != nil {
			return err
		}
		a.log.Debug("NodeID for this node is %s", nodeIDStr)
		nodeID, err := ids.ShortFromPrefixedString(nodeIDStr, constants.NodeIDPrefix)
		if err != nil {
			return fmt.Errorf("could not convert node id from string: %s", err)
		}
		// Map node name to the node
		a.nodes[n.Spec.DeploymentName] = &Node{
			uri:    uri,
			client: cli,
			name:   n.Spec.DeploymentName,
			nodeID: nodeID,
			k8sObj: n,
		}
		a.log.Debug("Name: %s, NodeID: %s, URI: %s", n.Spec.DeploymentName, nodeID, uri)
	}
	return nil
}

// String returns a string representing the network nodes
func (a *networkImpl) String() string {
	s := strings.Builder{}
	_, _ = s.WriteString("\n****************************************************************************************************\n")
	_, _ = s.WriteString("     List of nodes in the network: \n")
	_, _ = s.WriteString("  +------------------------------------------------------------------------------------------------+\n")
	_, _ = s.WriteString("  +  NodeID                           |     Label         |      Cluster URI                       +\n")
	_, _ = s.WriteString("  +------------------------------------------------------------------------------------------------+\n")
	for _, n := range a.nodes {
		s.WriteString(fmt.Sprintf("     %s    %s    %s\n", n.nodeID, n.name, n.uri))
	}
	s.WriteString("****************************************************************************************************\n")
	return s.String()
}
