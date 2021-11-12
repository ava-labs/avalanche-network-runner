package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ava-labs/avalanche-network-runner/api"
	"github.com/ava-labs/avalanche-network-runner/constants"
	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanchego/ids"
	avagoconst "github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/logging"
	"golang.org/x/sync/errgroup"

	k8sapi "github.com/ava-labs/avalanchego-operator/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	k8scli "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// How long we'll wait for a kubernetes pod to become reachable
	nodeReachableTimeout = 2 * time.Minute
	// Time between checks to see if a node is reachable
	nodeReachableRetryFreq = 3 * time.Second
	envVarPrefix           = "AVAGO_"
	genesisNetworkIDKey    = "networkID"
	paramNetworkID         = "network-id"
	envVarNetworkID        = "AVAGO_NETWORK_ID"
)

var (
	errNodeDoesNotExist = errors.New("Node with given NodeID does not exist")
	errStopped          = errors.New("network stopped")
)

// networkImpl is the kubernetes data type representing a kubernetes network adapter.
// It implements the network.Network interface
type networkImpl struct {
	config  network.Config
	k8scli  k8scli.Client
	kconfig *rest.Config
	// Node name --> The node
	nodes     map[string]*K8sNode
	beaconURL string
	// Closed when network is done shutting down
	closedOnStopCh chan struct{}
	log            logging.Logger
}

// Config encapsulates kubernetes specific options
type Config struct {
	Namespace      string `json:"namespace"`      // The kubernetes Namespace
	DeploymentSpec string `json:"deploymentSpec"` // Identifies this network in the cluster
	Kind           string `json:"kind"`           // Identifies the object Kind for the operator
	APIVersion     string `json:"apiVersion"`     // The APIVersion of the kubernetes object
	Image          string `json:"image"`          // The docker image to use
	Tag            string `json:"tag"`            // The docker tag to use
	Genesis        string `json:"genesis"`        // The genesis conf file for all nodes
	Certificate    []byte // The certificates for the nodes
	CertKey        []byte // The certificate keys for the nods
}

// TODO remove
// TODO should this just be a part of NewNetwork?
// NewAdapter creates a new adapter
// func newAdapter(conf network.Config, log logging.Logger) (*networkImpl, error) {

// }

// NewNetwork returns a new network whose initial state is specified in the config
func NewNetwork(conf network.Config, log logging.Logger) (network.Network, error) {
	// init k8s client
	scheme := runtime.NewScheme()
	if err := k8sapi.AddToScheme(scheme); err != nil {
		return nil, err
	}
	kubeconfig := ctrl.GetConfigOrDie()
	kubeClient, err := k8scli.New(kubeconfig, k8scli.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}
	log.Info("K8s client initialized")
	beacons, nonBeacons, err := createDeploymentFromConfig(conf.Genesis, conf.NodeConfigs)
	if err != nil {
		return nil, err
	}
	if len(beacons) == 0 {
		return nil, errors.New("NodeConfigs don't describe any beacon nodes")
	}
	net := &networkImpl{
		config:         conf,
		k8scli:         kubeClient,
		kconfig:        kubeconfig,
		closedOnStopCh: make(chan struct{}),
		log:            log,
		nodes:          make(map[string]*K8sNode, len(conf.NodeConfigs)),
	}
	// Start the beacon nodes
	if err := net.launchNodes(beacons); err != nil {
		return nil, fmt.Errorf("error launching beacons: %w", err)
	}
	// Tell future nodes the IP of the beacon node
	// TODO add support for multiple beacons
	net.beaconURL = beacons[0].Status.NetworkMembersURI[0]
	if net.beaconURL == "" {
		return nil, errors.New("Bootstrap URI is set to empty")
	}
	// Start the non-beacon nodes
	if err := net.launchNodes(nonBeacons); err != nil {
		return nil, fmt.Errorf("Error launching non-beacons: %s", err)
	}
	// Build a mapping from k8s URIs to names/ids
	if err := net.buildNodeMapping(append(beacons, nonBeacons...)); err != nil {
		return nil, err
	}

	net.log.Info("network: %s", net)
	return net, nil
}

// GetNodesNames returns an array of node names
func (a *networkImpl) GetNodesNames() []string {
	nodes := make([]string, len(a.nodes))
	i := 0
	for _, n := range a.nodes {
		nodes[i] = n.name
		i++
	}
	return nodes
}

// Healthy returns a channel which signals when the network is ready to be used
func (a *networkImpl) Healthy() chan error {
	errCh := make(chan error, 1)

	go func() {
		errGr, ctx := errgroup.WithContext(context.Background())
		for _, node := range a.nodes {
			node := node
			errGr.Go(func() error {
				// Every 5 seconds, query node for health status.
				// Do this up to 20 times.
				for i := 0; i < int(constants.HealthyTimeout/constants.HealthCheckFreq); i++ {
					select {
					case <-a.closedOnStopCh:
						return errStopped
					case <-ctx.Done():
						return nil
					case <-time.After(constants.HealthCheckFreq):
					}
					health, err := node.client.HealthAPI().Health()
					if err == nil && health.Healthy {
						a.log.Info("node %q became healthy", node.GetName())
						return nil
					}
				}
				return fmt.Errorf("node %q timed out on becoming healthy", node.GetName())
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

// Stop all the nodes
func (a *networkImpl) Stop(ctx context.Context) error {
	// delete network
	for s, n := range a.nodes {
		a.log.Debug("Shutting down node %s...", s)
		if err := a.k8scli.Delete(ctx, n.k8sObj); err != nil {
			// TODO don't we want to continue deleting
			// nodes here, even if there is an error?
			return err
		}
	}
	close(a.closedOnStopCh)
	a.log.Info("Network cleared")
	return nil
}

// AddNode starts a new node with the config
func (a *networkImpl) AddNode(cfg node.Config) (node.Node, error) {
	node, err := buildK8sObj(a.config.Genesis, cfg)
	if err != nil {
		return nil, err
	}
	if err := a.k8scli.Create(context.TODO(), node); err != nil {
		return nil, err
	}

	if err := a.launchNodes([]*k8sapi.Avalanchego{node}); err != nil {
		return nil, err
	}

	uri := node.Status.NetworkMembersURI[0]
	cli := api.NewAPIClient(uri, constants.DefaultPort, constants.APITimeoutDuration)
	nid, err := cli.InfoAPI().GetNodeID()
	if err != nil {
		return nil, err
	}
	a.log.Debug("NodeID for this node is %s", nid)
	nodeID, err := ids.ShortFromPrefixedString(nid, avagoconst.NodeIDPrefix)
	if err != nil {
		return nil, fmt.Errorf("could not convert node id from string: %s", err)
	}
	return &K8sNode{
		uri:    uri,
		client: cli,
		name:   node.Spec.DeploymentName,
		nodeID: nodeID,
		k8sObj: node,
	}, nil
}

// RemoveNode stops the node with this ID.
func (a *networkImpl) RemoveNode(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if p, ok := a.nodes[id]; ok {
		if err := a.k8scli.Delete(ctx, p.k8sObj); err != nil {
			return err
		}
		return nil
	}
	return errNodeDoesNotExist
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

// GetNode returns the node with this ID.
func (a *networkImpl) GetNode(id string) (node.Node, error) {
	if n, ok := a.nodes[id]; ok {
		return n, nil
	}
	return nil, errNodeDoesNotExist
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
		if err := a.k8scli.Create(context.TODO(), n); err != nil {
			return err
		}

		a.log.Debug("Waiting for pod to be created...")
		for len(n.Status.NetworkMembersURI) != 1 {
			err := a.k8scli.Get(context.TODO(), types.NamespacedName{
				Name:      n.Name,
				Namespace: n.Namespace,
			}, n)
			if err != nil {
				return err
			}
			select {
			case <-time.After(5 * time.Second):
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
			fmturi := fmt.Sprintf("http://%s:%d", node.Status.NetworkMembersURI[0], constants.DefaultPort)
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					a.log.Debug("checking if %s is reachable...", fmturi)
					// TODO is there a better way to wait until the node is reachable?
					if _, err := http.Get(fmturi); err == nil {
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

// Takes the genesis of a network and node configs and returns:
// 1) The beacon nodes
// 2) The non-beacon nodes
// as avalanchego-operator compatible descriptions.
// May return nil slices.
func createDeploymentFromConfig(genesis []byte, nodeConfigs []node.Config) ([]*k8sapi.Avalanchego, []*k8sapi.Avalanchego, error) {
	var beacons, nonBeacons []*k8sapi.Avalanchego
	for _, c := range nodeConfigs {
		spec, err := buildK8sObj(genesis, c)
		if err != nil {
			return nil, nil, err
		}
		if c.IsBeacon {
			beacons = append(beacons, spec)
			continue
		}
		nonBeacons = append(nonBeacons, spec)
	}
	return beacons, nonBeacons, nil
}

// Takes a node's config and genesis and returns the node
// as a Kubernetes spec
func buildK8sObj(genesis []byte, c node.Config) (*k8sapi.Avalanchego, error) {
	env, err := buildNodeEnv(genesis, c)
	if err != nil {
		return nil, err
	}
	certs := []k8sapi.Certificate{
		{
			Cert: base64.StdEncoding.EncodeToString(c.StakingCert),
			Key:  base64.StdEncoding.EncodeToString(c.StakingKey),
		},
	}
	k8sConf, ok := c.ImplSpecificConfig.(Config)
	if !ok {
		return nil, fmt.Errorf("expected Config but got %T", c.ImplSpecificConfig)
	}

	return &k8sapi.Avalanchego{
		TypeMeta: metav1.TypeMeta{
			Kind:       k8sConf.Kind,
			APIVersion: k8sConf.APIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      k8sConf.DeploymentSpec,
			Namespace: k8sConf.Namespace,
		},
		Spec: k8sapi.AvalanchegoSpec{
			BootstrapperURL: "",
			DeploymentName:  c.Name,
			Image:           k8sConf.Image,
			Tag:             k8sConf.Tag,
			Env:             env,
			NodeCount:       1,
			Certificates:    certs,
			Genesis:         string(genesis),
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(resourceLimitsCPU), // TODO: Should these be supplied by Opts rather than const?
					corev1.ResourceMemory: resource.MustParse(resourceLimitsMemory),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(resourceRequestCPU),
					corev1.ResourceMemory: resource.MustParse(resourceRequestMemory),
				},
			},
		},
	}, nil
}

// Given a node's config and genesis, returns the environment
// variables (i.e. config flags) to give to the node
func buildNodeEnv(genesis []byte, c node.Config) ([]corev1.EnvVar, error) {
	var avagoConf map[string]interface{} // AvalancheGo config file as a map
	if err := json.Unmarshal(c.ConfigFile, &avagoConf); err != nil {
		return nil, err
	}
	networkID, err := getNetworkID(genesis)
	if err != nil {
		return nil, err
	}

	// For each config flag, convert it to the format
	// AvalancheGo expects environment variable config flags in.
	// e.g. bootstrap-ips --> AVAGO_BOOTSTRAP_IPS
	// e.g. log-level --> AVAGO_LOG_LEVEL
	env := make([]corev1.EnvVar, 0, len(avagoConf)+1)
	for key, val := range avagoConf {
		// we use the network id from genesis -- ignore the one in config
		if key == paramNetworkID {
			// TODO make sure config's network ID doesn't conflict with genesis
			continue
		}
		v := corev1.EnvVar{
			Name:  convertKey(key),
			Value: val.(string),
		}
		env = append(env, v)
	}
	// Provide environment variable giving the network ID
	v := corev1.EnvVar{
		Name:  envVarNetworkID,
		Value: fmt.Sprint(networkID),
	}
	env = append(env, v)
	return env, nil
}

// Updates [a.nodes] to incluse [nodes]
func (a *networkImpl) buildNodeMapping(nodes []*k8sapi.Avalanchego) error {
	for _, n := range nodes {
		uri := n.Status.NetworkMembersURI[0]
		a.log.Debug("creating network node and client for %s", uri)
		cli := api.NewAPIClient(uri, constants.DefaultPort, constants.APITimeoutDuration)
		nodeIDStr, err := cli.InfoAPI().GetNodeID()
		if err != nil {
			return err
		}
		a.log.Debug("NodeID for this node is %s", nodeIDStr)
		nodeID, err := ids.ShortFromPrefixedString(nodeIDStr, avagoconst.NodeIDPrefix)
		if err != nil {
			return fmt.Errorf("could not convert node id from string: %s", err)
		}
		// Map node name to the node
		a.nodes[n.Spec.DeploymentName] = &K8sNode{
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

// Convert a config flag to the format AvalancheGo expects
// environment variable config flags in.
// e.g. bootstrap-ips --> AVAGO_BOOTSTRAP_IPS
// e.g. log-level --> AVAGO_LOG_LEVEL
func convertKey(key string) string {
	key = strings.Replace(key, "-", "_", -1)
	key = strings.ToUpper(key)
	newKey := fmt.Sprintf("%s%s", envVarPrefix, key)
	return newKey
}

// Returns the networkID in the given genesis file
func getNetworkID(genesisBytes []byte) (float64, error) {
	var genesis map[string]interface{}
	if err := json.Unmarshal(genesisBytes, &genesis); err != nil {
		return -1, err
	}
	networkIDIntf, ok := genesis[genesisNetworkIDKey]
	if !ok {
		return 0, errors.New("genesis doesn't have network ID")
	}
	networkID, ok := networkIDIntf.(float64)
	if !ok {
		return 0, fmt.Errorf("expected flaot64 but got %T", networkIDIntf)
	}
	return networkID, nil
}
