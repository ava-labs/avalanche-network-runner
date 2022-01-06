package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// networkParams encapsulate params to create a network
type networkParams struct {
	conf          network.Config
	log           logging.Logger
	k8sClient     k8scli.Client
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
	// Must be held when [nodes.lock] is accessed
	nodesLock sync.RWMutex
	// Node name --> The node.
	// If there is a running k8s pod for a node, it's in [nodes]
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

// If this function returns a nil error, you *must* eventually call
// Stop() on the returned network. Failure to do so will cause old
// state to linger in k8s.
func newNetwork(params networkParams) (network.Network, error) {
	beacons, nonBeacons, err := createDeploymentFromConfig(params)
	if err != nil {
		return nil, err
	}
	if len(beacons) == 0 {
		return nil, errors.New("NodeConfigs don't have any beacon nodes")
	}
	net := &networkImpl{
		config:         params.conf,
		k8scli:         params.k8sClient,
		closedOnStopCh: make(chan struct{}),
		log:            params.log,
		nodes:          make(map[string]*Node, len(params.conf.NodeConfigs)),
		dnsChecker:     params.dnsChecker,
		apiClientFunc:  params.apiClientFunc,
	}
	net.log.Debug("launching beacon nodes...")
	// Start the beacon nodes and wait until they're reachable
	if err := net.launchNodes(beacons); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if err := net.Stop(ctx); err != nil {
			net.log.Warn("error stopping network: %s", err)
		}
		return nil, fmt.Errorf("error launching beacons: %w", err)
	}
	// Tell future nodes the IP of the beacon node
	// TODO add support for multiple beacons
	// TODO don't rely on [beacons] being updated in launchNodes.
	//      It's not a very clean pattern.
	net.beaconURL = beacons[0].Status.NetworkMembersURI[0]
	if net.beaconURL == "" {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if err := net.Stop(ctx); err != nil {
			net.log.Warn("error stopping network: %s", err)
		}
		return nil, errors.New("Bootstrap URI is set to empty")
	}
	net.log.Info("Beacon node started")
	// Start the non-beacon nodes and wait until they're reachable
	if err := net.launchNodes(nonBeacons); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if err := net.Stop(ctx); err != nil {
			net.log.Warn("error stopping network: %s", err)
		}
		return nil, fmt.Errorf("Error launching non-beacons: %s", err)
	}
	net.log.Info("All nodes started. Network: %s", net)
	return net, nil
}

// NewNetwork returns a new network whose initial state is specified in the config
func NewNetwork(log logging.Logger, conf network.Config) (network.Network, error) {
	k8sClient, err := newK8sClient()
	if err != nil {
		return nil, fmt.Errorf("couldn't create k8s client: %w", err)
	}
	return newNetwork(networkParams{
		conf:      conf,
		log:       log,
		k8sClient: k8sClient,
		// TODO is there a better way to wait until the node is reachable?
		dnsChecker:    &defaultDNSReachableChecker{},
		apiClientFunc: api.NewAPIClient,
	})
}

// See network.Network
func (a *networkImpl) GetNodeNames() ([]string, error) {
	a.nodesLock.RLock()
	defer a.nodesLock.RUnlock()

	if a.isStopped() {
		return nil, network.ErrStopped
	}

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
	a.nodesLock.RLock()
	defer a.nodesLock.RUnlock()

	errCh := make(chan error, 1)
	if a.isStopped() {
		errCh <- network.ErrStopped
		return errCh
	}
	nodes := make([]*Node, 0, len(a.nodes))
	for _, node := range a.nodes {
		nodes = append(nodes, node)
	}

	go func() {
		errGr, ctx := errgroup.WithContext(ctx)
		for _, node := range nodes {
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
					health, err := node.apiClient.HealthAPI().Health()
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
	a.nodesLock.Lock()
	defer a.nodesLock.Unlock()

	if a.isStopped() {
		return network.ErrStopped
	}

	failCount := 0
	for nodeName, node := range a.nodes {
		a.log.Debug("Shutting down node %q...", nodeName)
		if err := a.k8scli.Delete(ctx, node.k8sObjSpec); err != nil {
			a.log.Error("error while stopping node %s: %s", node.name, err)
			failCount++
		}
		delete(a.nodes, nodeName)
	}
	close(a.closedOnStopCh)
	if failCount > 0 {
		return fmt.Errorf("%d nodes failed shutting down", failCount)
	}
	a.log.Info("Network stopped")
	return nil
}

// AddNode starts a new node with the given config and blocks
// until it is reachable.
// Assumes [a.nodesLock] isn't held.
func (a *networkImpl) AddNode(cfg node.Config) (node.Node, error) {
	a.nodesLock.RLock()
	isStopped := a.isStopped()
	a.nodesLock.RUnlock()

	// TODO fix this is race condition:
	// Stop() can be called after the lock is released above.
	// If that happens, we'll create the pod inside launchNodes
	// and then never destroy it.
	// launchNodes assumes [a.nodesLock] isn't held so we can't just
	// hold the lock inside this method to fix this race condition.
	if isStopped {
		return nil, network.ErrStopped
	}

	nodeSpec, err := buildK8sObjSpec([]byte(a.config.Genesis), cfg)
	if err != nil {
		return nil, err
	}

	a.log.Debug("Launching new node %s to network...", cfg.Name)
	if err := a.launchNodes([]*k8sapi.Avalanchego{nodeSpec}); err != nil {
		return nil, err
	}

	a.nodesLock.RLock()
	defer a.nodesLock.RUnlock()
	return a.nodes[nodeSpec.Name], nil
}

// See network.Network
func (a *networkImpl) RemoveNode(name string) error {
	a.nodesLock.Lock()
	defer a.nodesLock.Unlock()

	if a.isStopped() {
		return network.ErrStopped
	}

	ctx, cancel := context.WithTimeout(context.Background(), removeTimeout)
	defer cancel()

	if node, ok := a.nodes[name]; ok {
		if err := a.k8scli.Delete(ctx, node.k8sObjSpec); err != nil {
			return err
		}
		a.log.Info("Removed node %q", name)
		delete(a.nodes, name)
		return nil
	}
	return fmt.Errorf("node %q not found", name)
}

// GetAllNodes returns all nodes
func (a *networkImpl) GetAllNodes() (map[string]node.Node, error) {
	a.nodesLock.RLock()
	defer a.nodesLock.RUnlock()

	if a.isStopped() {
		return nil, network.ErrStopped
	}

	nodesCopy := make(map[string]node.Node, len(a.nodes))
	for nodeName, node := range a.nodes {
		nodesCopy[nodeName] = node
	}
	return nodesCopy, nil
}

// See network.Network
func (a *networkImpl) GetNode(name string) (node.Node, error) {
	a.nodesLock.RLock()
	defer a.nodesLock.RUnlock()

	if a.isStopped() {
		return nil, network.ErrStopped
	}
	if n, ok := a.nodes[name]; ok {
		return n, nil
	}
	return nil, fmt.Errorf("node %q not found", name)
}

// Assumes [a.nodesLock] is held
func (net *networkImpl) isStopped() bool {
	select {
	case <-net.closedOnStopCh:
		return true
	default:
		return false
	}
}

// Creates the given nodes and blocks until they're all reachable.
// Assumes [a.nodesLock] isn't held.
func (a *networkImpl) launchNodes(nodeSpecs []*k8sapi.Avalanchego) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errGr, ctx := errgroup.WithContext(ctx)
	for _, nodeSpec := range nodeSpecs {
		nodeSpec := nodeSpec
		errGr.Go(func() error {
			if err := a.launchNode(ctx, nodeSpec); err != nil {
				return fmt.Errorf("error launching node %q: %w", nodeSpec.Spec.DeploymentName, err)
			}
			return nil
		})
	}
	return errGr.Wait()
}

// Create the given node in k8s and block until it's reachable.
// Assumes [a.nodesLock] isn't held.
func (a *networkImpl) launchNode(ctx context.Context, nodeSpec *k8sapi.Avalanchego) error {
	ctx, cancel := context.WithTimeout(ctx, nodeReachableTimeout)
	defer cancel()

	a.nodesLock.Lock()
	if a.beaconURL != "" {
		nodeSpec.Spec.BootstrapperURL = a.beaconURL
	}
	// Update [a.nodes] so that we'll delete this node on stop
	a.nodes[nodeSpec.Spec.DeploymentName] = &Node{
		name:       nodeSpec.Spec.DeploymentName,
		k8sObjSpec: nodeSpec,
	}
	// Create a Kubernetes pod for this node
	err := a.k8scli.Create(ctx, nodeSpec)
	a.nodesLock.Unlock()
	if err != nil {
		return fmt.Errorf("k8scli.Create failed: %w", err)
	}

	a.log.Debug("Waiting for pod to be created for node %q...", nodeSpec.Spec.DeploymentName)
	for len(nodeSpec.Status.NetworkMembersURI) != 1 {
		if err := a.k8scli.Get(ctx, types.NamespacedName{
			Name:      nodeSpec.Name,
			Namespace: nodeSpec.Namespace,
		}, nodeSpec); err != nil {
			return fmt.Errorf("k8scli.Get failed: %w", err)
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
	url := nodeSpec.Status.NetworkMembersURI[0]
	apiURL := fmt.Sprintf("http://%s:%d", url, defaultAPIPort)
reachableLoop:
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			a.log.Debug("checking if %q is reachable at %s...", nodeSpec.Spec.DeploymentName, apiURL)
			if reachable := a.dnsChecker.Reachable(ctx, apiURL); reachable {
				a.log.Debug("%q has become reachable", nodeSpec.Spec.DeploymentName)
				break reachableLoop
			}
			// Wait before checking again
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(nodeReachableRetryFreq):
			}
		}
	}

	// Create an API client
	a.log.Debug("creating network node and client for %s", url)
	apiClient := a.apiClientFunc(url, defaultAPIPort, apiTimeout)
	// Get this node's ID
	// TODO should we get this by parsing the key/cert?
	nodeIDStr, err := apiClient.InfoAPI().GetNodeID()
	if err != nil {
		return fmt.Errorf("couldn't get node ID: %w", err)
	}
	nodeID, err := ids.ShortFromPrefixedString(nodeIDStr, constants.NodeIDPrefix)
	if err != nil {
		return fmt.Errorf("could not parse node ID %q from string: %s", nodeIDStr, err)
	}
	// Update node info
	a.nodesLock.Lock()
	a.nodes[nodeSpec.Spec.DeploymentName].uri = url
	a.nodes[nodeSpec.Spec.DeploymentName].apiClient = apiClient
	a.nodes[nodeSpec.Spec.DeploymentName].nodeID = nodeID
	a.nodesLock.Unlock()
	a.log.Debug("Name: %s, NodeID: %s, URI: %s", nodeSpec.Spec.DeploymentName, nodeID, url)
	return nil
}

// String returns a string representing the network nodes
func (a *networkImpl) String() string {
	a.nodesLock.RLock()
	defer a.nodesLock.RUnlock()

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
