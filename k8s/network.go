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
	// Prefix the avalanchego-operator uses to pass params to avalanchego nodes
	envVarPrefix = "AVAGO_"
	// key for the network ID in the genesis file
	genesisNetworkIDKey = "networkID"
	// key for the network ID for the avalanchego node
	paramNetworkID = "network-id"
	// key for the network ID for the avalanchego-operator
	envVarNetworkID = "AVAGO_NETWORK_ID"
)

var (
	errNodeDoesNotExist = errors.New("Node with given NodeID does not exist")
	errStopped          = errors.New("network stopped")
)

// networkImpl is the kubernetes data type representing a kubernetes network adapter.
// It implements the network.Network interface
type networkImpl struct {
	config          network.Config
	k8scli          k8scli.Client
	kconfig         *rest.Config
	nodes           map[string]*K8sNode
	bootstrapperURL string
	// Closed when network is done shutting down
	closedOnStopCh chan struct{}
	log            logging.Logger
}

// Spec encapsulates kubernetes specific options
type Spec struct {
	Namespace   string `json:"namespace"`  // The kubernetes Namespace
	Identifier  string `json:"identifier"` // Identifies this network in the cluster
	Kind        string `json:"kind"`       // Identifies the object Kind for the operator
	APIVersion  string `json:"apiVersion"` // The APIVersion of the kubernetes object
	Image       string `json:"image"`      // The docker image to use
	Tag         string `json:"tag"`        // The docker tag to use
	Genesis     string `json:"genesis"`    // The genesis conf file for all nodes
	Certificate []byte // The certificates for the nodes
	CertKey     []byte // The certificate keys for the nods
}

// NewAdapter creates a new adapter
func newAdapter(conf network.Config, log logging.Logger) (*networkImpl, error) {
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

	return &networkImpl{
		config:         conf,
		k8scli:         kubeClient,
		kconfig:        kubeconfig,
		closedOnStopCh: make(chan struct{}),
		log:            log,
		nodes:          make(map[string]*K8sNode, len(conf.NodeConfigs)),
	}, nil
}

// NewNetwork returns a new network whose initial state is specified in the config
func NewNetwork(conf network.Config, log logging.Logger) (network.Network, error) {
	a, err := newAdapter(conf, log)
	if err != nil {
		return nil, err
	}
	a.log.Info("K8s client initialized")
	beacons, instances, err := a.createDeploymentFromConfig()
	if err != nil {
		return nil, err
	}
	if len(beacons) == 0 {
		return nil, errors.New("NodeConfigs don't describe any beacon node")
	}
	if err := a.launchNodes(beacons); err != nil {
		log.Fatal("Error launching beacons: %s", err)
		return nil, err
	}
	a.log.Info("Bootstrap node(s) started")
	// only one boostrap address supported for now
	a.bootstrapperURL = beacons[0].Status.NetworkMembersURI[0]
	if a.bootstrapperURL == "" {
		return nil, errors.New("Bootstrap URI is set to empty")
	}
	if err := a.launchNodes(instances); err != nil {
		log.Fatal("Error launching beacons: %s", err)
		return nil, err
	}
	a.log.Info("All nodes started")
	allNodes := append(beacons, instances...)
	// build a mapping from the given k8s URIs to names/ids
	if err := a.buildNodeMapping(allNodes); err != nil {
		return nil, err
	}

	a.log.Info("Network is up: %s", a)
	return a, nil
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
	failCount := 0
	for s, n := range a.nodes {
		a.log.Debug("Shutting down node %s...", s)
		err := a.k8scli.Delete(ctx, n.k8sObj)
		if err != nil {
			a.log.Warn("Error shutting down node %s: %s", s, err)
			failCount++
			// just continue shutting down other nodes
		}
	}
	close(a.closedOnStopCh)
	if failCount > 0 {
		return fmt.Errorf("%d nodes failed shutting down", failCount)
	}
	a.log.Info("Network cleared")
	return nil
}

// AddNode starts a new node with the config
func (a *networkImpl) AddNode(cfg node.Config) (node.Node, error) {
	node, err := a.buildK8sObj(cfg)
	if err != nil {
		return nil, err
	}
	a.log.Debug("Adding new node %s to network...", cfg.Name)
	if err := a.k8scli.Create(context.TODO(), node); err != nil {
		return nil, err
	}

	a.log.Debug("Launching new node %s to network...", cfg.Name)
	if err := a.launchNodes([]*k8sapi.Avalanchego{node}); err != nil {
		return nil, err
	}

	uri := node.Status.NetworkMembersURI[0]
	cli := api.NewAPIClient(uri, constants.DefaultPort, constants.APITimeoutDuration)
	nid, err := cli.InfoAPI().GetNodeID()
	if err != nil {
		return nil, err
	}
	a.log.Debug("Succesful. NodeID for this node is %s", nid)
	nodeID, err := ids.ShortFromPrefixedString(nid, avagoconst.NodeIDPrefix)
	if err != nil {
		return nil, fmt.Errorf("could not convert node id from string: %s", err)
	}
	apiNode := &K8sNode{
		uri:    uri,
		client: cli,
		name:   node.Spec.DeploymentName,
		nodeID: nodeID,
		k8sObj: node,
	}
	return apiNode, nil
}

// RemoveNode stops the node with this ID.
func (a *networkImpl) RemoveNode(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if p, ok := a.nodes[id]; ok {
		if err := a.k8scli.Delete(ctx, p.k8sObj); err != nil {
			return err
		}
		a.log.Info("Removed node %s", p)
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

func (a *networkImpl) launchNodes(nodes []*k8sapi.Avalanchego) error {
	ctx, cancel := context.WithTimeout(context.Background(), nodeReachableTimeout)
	defer cancel()
	errGr, ctx := errgroup.WithContext(ctx)
	for _, n := range nodes {
		if a.bootstrapperURL != "" {
			n.Spec.BootstrapperURL = a.bootstrapperURL
		}
		if err := a.k8scli.Create(context.TODO(), n); err != nil {
			return err
		}

		a.log.Debug("Waiting for pods to be created...")
		for len(n.Status.NetworkMembersURI) != 1 {
			err := a.k8scli.Get(context.TODO(), types.NamespacedName{
				Name:      n.Name,
				Namespace: n.Namespace,
			}, n)
			if err != nil {
				return err
			}
			time.Sleep(5 * time.Second)
		}

		a.log.Debug("Created. Waiting to be reachable...")
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

// createDeploymentFromConfig reads the config file(s) and creates the required kubernetes deployment objects
func (a *networkImpl) createDeploymentFromConfig() ([]*k8sapi.Avalanchego, []*k8sapi.Avalanchego, error) {
	// Returns a new network whose initial state is specified in the config
	instances := make([]*k8sapi.Avalanchego, 0)
	beacons := make([]*k8sapi.Avalanchego, 0)

	for _, c := range a.config.NodeConfigs {
		spec, err := a.buildK8sObj(c)
		if err != nil {
			return nil, nil, err
		}
		if c.IsBeacon {
			beacons = append(beacons, spec)
			continue
		}
		instances = append(instances, spec)
	}
	return beacons, instances, nil
}

// buildK8sObj builds an object representing a node for the kubernetes cluster.
// It takes a node.Config object which defines the required parameters and returns
// an object deployable on kubernetes
func (a *networkImpl) buildK8sObj(c node.Config) (*k8sapi.Avalanchego, error) {
	env, err := a.buildNodeEnv(c)
	if err != nil {
		return nil, err
	}
	certs := []k8sapi.Certificate{
		{
			Cert: base64.StdEncoding.EncodeToString(c.StakingCert),
			Key:  base64.StdEncoding.EncodeToString(c.StakingKey),
		},
	}
	k8sConf, ok := c.ImplSpecificConfig.(Spec)
	if !ok {
		return nil, fmt.Errorf("Expected Spec but got %T", c.ImplSpecificConfig)
	}

	k8sConf.Identifier = c.Name

	return &k8sapi.Avalanchego{
		TypeMeta: metav1.TypeMeta{
			Kind:       k8sConf.Kind,
			APIVersion: k8sConf.APIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      k8sConf.Identifier,
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
			Genesis:         string(a.config.Genesis),
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

// buildNodeEnv builds the environment variables array required by the avalanchego-operator
// which then passes these on to the actual avalanchego nodes.
// It takes a node.Config describing a node and returns an array of environment variables
// with the type required by the operator.
func (a *networkImpl) buildNodeEnv(c node.Config) ([]corev1.EnvVar, error) {
	networkID, err := getNetworkID(string(a.config.Genesis))
	if err != nil {
		return []corev1.EnvVar{}, err
	}
	env := make([]corev1.EnvVar, 0)
	if c.ConfigFile != nil {
		var avagoConf map[string]interface{}
		if err := json.Unmarshal(c.ConfigFile, &avagoConf); err != nil {
			return []corev1.EnvVar{}, err
		}
		for key, val := range avagoConf {
			// we set the same network-id from genesis to avoid conflicts
			if key == paramNetworkID {
				continue
			}
			v := corev1.EnvVar{
				Name:  convertKey(key),
				Value: val.(string),
			}
			env = append(env, v)
		}
	}
	v := corev1.EnvVar{
		Name:  envVarNetworkID,
		Value: fmt.Sprint(networkID),
	}
	env = append(env, v)

	return env, nil
}

// buildNodeMapping creates the actual k8s node representation and creates the nodes map
// with the name as the key. It also establishes connection via the API client.
func (a *networkImpl) buildNodeMapping(nodes []*k8sapi.Avalanchego) error {
	for _, n := range nodes {
		uri := n.Status.NetworkMembersURI[0]
		a.log.Debug("creating network node and client for %s", uri)
		cli := api.NewAPIClient(uri, constants.DefaultPort, constants.APITimeoutDuration)
		nid, err := cli.InfoAPI().GetNodeID()
		if err != nil {
			return err
		}
		a.log.Debug("NodeID for this node is %s", nid)
		nodeID, err := ids.ShortFromPrefixedString(nid, avagoconst.NodeIDPrefix)
		if err != nil {
			return fmt.Errorf("could not convert node id from string: %s", err)
		}
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

// String returns a string representing the network nodes
func (a *networkImpl) String() string {
	s := strings.Builder{}
	_, _ = s.WriteString("\n****************************************************************************************************\n")
	_, _ = s.WriteString("     List of nodes in the network: \n")
	_, _ = s.WriteString("  +------------------------------------------------------------------------------------------------+\n")
	_, _ = s.WriteString("  +  NodeID                           |     Label         |      Cluster URI                       +\n")
	_, _ = s.WriteString("  +------------------------------------------------------------------------------------------------+\n")
	for _, n := range a.nodes {
		s.WriteString(fmt.Sprintf("     %s    %s         %s\n", n.nodeID, n.name, n.uri))
	}
	s.WriteString("****************************************************************************************************\n")
	return s.String()
}

// convertKey converts an avalanchego parameter as required for an avalanchego node config file,
// to the format required by the operator: AVAGO_[ALL_UPPER_CASE_DASH_REPLACED_BY_UNDERSCORE]
func convertKey(key string) string {
	key = strings.Replace(key, "-", "_", -1)
	key = strings.ToUpper(key)
	newKey := fmt.Sprintf("%s%s", envVarPrefix, key)
	return newKey
}

// getNetworkID returns the networkID contained in the genesis string
func getNetworkID(genesisStr string) (float64, error) {
	var genesis map[string]interface{}
	if err := json.Unmarshal([]byte(genesisStr), &genesis); err != nil {
		return -1, err
	}
	for k, v := range genesis {
		if k == genesisNetworkIDKey {
			return v.(float64), nil
		}
	}
	return -1, errors.New("No network-id config key found in genesis")
}
