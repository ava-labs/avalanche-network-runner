package k8s

import (
	"context"
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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	k8scli "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// How long we'll wait for a kubernetes pod to become reachable
	nodeReachableTimeout = 2 * time.Minute
	// Time between checks to see if a node is reachable
	nodeReachableRetryFreq = 3 * time.Second
)

var (
	errNodeDoesNotExist = errors.New("Node with given NodeID does not exist")
)

// networkImpl is the kubernetes data type representing a kubernetes network adapter.
// It implements the network.Network interface
type networkImpl struct {
	config     network.Config
	k8sConfig  Config
	k8sNetwork *k8sapi.Avalanchego
	k8scli     k8scli.Client
	kconfig    *rest.Config
	cs         *kubernetes.Clientset
	nodes      map[string]*K8sNode
	pods       map[string]corev1.Pod
	// Closed when network is done shutting down
	closedOnStopCh chan struct{}
	log            logging.Logger
}

// Config encapsulates kubernetes specific options
type Config struct {
	Namespace      string // The kubernetes Namespace
	DeploymentSpec string // Identifies this network in the cluster
	Kind           string // Identifies the object Kind for the operator
	APIVersion     string // The APIVersion of the kubernetes object
	Image          string // The docker image to use
	Tag            string // The docker tag to use
	LogLevelKey    string // The key for the log level value
}

// TODO should this just be a part of NewNetwork?
// NewAdapter creates a new adapter
func newAdapter(opts Config) (*networkImpl, error) {
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
		k8scli:         kubeClient,
		k8sConfig:      opts,
		kconfig:        kubeconfig,
		closedOnStopCh: make(chan struct{}),
	}, nil
}

// NewNetwork returns a new network whose initial state is specified in the config
func NewNetwork(config network.Config, log logging.Logger) (network.Network, error) {
	k8sconf, ok := config.ImplSpecificConfig.(Config)
	if !ok {
		return nil, errors.New("Incompatible network config object")
	}
	a, err := newAdapter(k8sconf)
	if err != nil {
		return nil, err
	}
	a.log = log
	a.log.Info("K8s client initialized")
	a.config = config
	a.k8sNetwork = a.createDeploymentFromConfig()
	if err := a.k8scli.Create(context.TODO(), a.k8sNetwork); err != nil {
		return nil, err
	}

	a.nodes = make(map[string]*K8sNode, config.NodeCount)
	a.pods = make(map[string]corev1.Pod, config.NodeCount)

	a.log.Debug("Waiting for pods to be created...")
	for len(a.k8sNetwork.Status.NetworkMembersURI) != a.k8sNetwork.Spec.NodeCount {
		err := a.k8scli.Get(context.TODO(), types.NamespacedName{
			Name:      a.k8sNetwork.Name,
			Namespace: a.k8sConfig.Namespace,
		}, a.k8sNetwork)
		if err != nil {
			return nil, err
		}
		time.Sleep(5 * time.Second)
	}

	// use the last uri to try connecting to it until it resolves
	// otherwise we have to sleep indiscriminately, we can't just use the API right away:
	// the kubernetes cluster has already created the pods but not the DNS names,
	// so using the API Client too early results in an error.
	testuri := a.k8sNetwork.Status.NetworkMembersURI[config.NodeCount-1]
	fmturi := fmt.Sprintf("http://%s:%d", testuri, constants.DefaultPort)
	ctx, cancel := context.WithTimeout(context.Background(), nodeReachableTimeout)
	defer cancel()
LOOP:
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			a.log.Debug("checking if %s is reachable...", fmturi)
			// TODO is there a better way to wait until the node is reachable?
			if _, err := http.Get(fmturi); err == nil {
				a.log.Debug("%s has become reachable", fmturi)
				break LOOP
			}
			// Wait before checking again
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(nodeReachableRetryFreq):
			}
		}
	}

	// build a mapping from the given k8s URIs to names/ids
	if err := a.buildNodeMapping(); err != nil {
		return nil, err
	}

	a.log.Info("network: %s", a)
	return a, nil
}

// GetNodesNames returns an array of node names
func (a *networkImpl) GetNodesNames() ([]string, error) {
	nodes := make([]string, len(a.nodes))
	i := 0
	for _, n := range a.nodes {
		nodes[i] = n.name
		i++
	}
	return nodes, nil
}

// Healthy returns a channel which signals when the network is ready to be used
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
						if err := ctx.Err(); err != nil {
							return fmt.Errorf("node %q failed to become healthy: %w", node.GetName(), err)
						}
						return nil
					case <-time.After(constants.HealthCheckInterval):
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

// Stop all the nodes
func (a *networkImpl) Stop(ctx context.Context) error {
	// delete network
	err := a.k8scli.Delete(ctx, a.k8sNetwork)
	if err != nil {
		return err
	}
	close(a.closedOnStopCh)
	a.log.Info("Network cleared")
	return nil
}

// AddNode starts a new node with the config
func (a *networkImpl) AddNode(cfg node.Config) (node.Node, error) {
	node := &k8sapi.Avalanchego{
		TypeMeta: metav1.TypeMeta{
			Kind:       a.k8sConfig.Kind,
			APIVersion: a.k8sConfig.APIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: a.k8sConfig.Namespace,
		},
		Spec: k8sapi.AvalanchegoSpec{
			DeploymentName:  cfg.Name,
			BootstrapperURL: a.k8sNetwork.Status.BootstrapperURL,
			NodeCount:       1,
			Image:           a.k8sNetwork.Spec.Image,
			Tag:             a.k8sNetwork.Spec.Tag,
			Env:             a.k8sNetwork.Spec.Env,
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
	}
	if err := a.k8scli.Create(context.TODO(), node); err != nil {
		return nil, err
	}
	apiNode := &K8sNode{}
	return apiNode, nil
}

// RemoveNode stops the node with this ID.
func (a *networkImpl) RemoveNode(id string) error {
	if p, ok := a.pods[id]; ok {
		if err := a.cs.CoreV1().Nodes().Delete(context.TODO(), p.Name, metav1.DeleteOptions{}); err != nil {
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

func (a *networkImpl) createDeploymentFromConfig() *k8sapi.Avalanchego {
	// Returns a new network whose initial state is specified in the config
	newChain := &k8sapi.Avalanchego{
		TypeMeta: metav1.TypeMeta{
			Kind:       a.k8sConfig.Kind,
			APIVersion: a.k8sConfig.APIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.k8sConfig.DeploymentSpec,
			Namespace: a.k8sConfig.Namespace,
		},
		Spec: k8sapi.AvalanchegoSpec{
			DeploymentName: a.config.Name,
			NodeCount:      a.config.NodeCount,
			Image:          a.k8sConfig.Image,
			Tag:            a.k8sConfig.Tag,
			Env: []corev1.EnvVar{
				{
					Name:  a.k8sConfig.LogLevelKey,
					Value: a.config.LogLevel,
				},
			},
		},
	}

	return newChain
}

// buildNodeMapping creates the actual k8s node representation and creates the nodes map
// with the name as the key
func (a *networkImpl) buildNodeMapping() error {
	for i, u := range a.k8sNetwork.Status.NetworkMembersURI {
		a.log.Debug("creating network node and client for %s", u)
		cli := api.NewAPIClient(u, constants.DefaultPort, constants.APITimeoutDuration)
		nid, err := cli.InfoAPI().GetNodeID()
		if err != nil {
			return err
		}
		a.log.Debug("NodeID for this node is %s", nid)
		name := fmt.Sprintf("validator-%d", i)
		nodeID, err := ids.ShortFromPrefixedString(nid, avagoconst.NodeIDPrefix)
		if err != nil {
			return fmt.Errorf("could not convert node id from string: %s", err)
		}
		a.nodes[name] = &K8sNode{
			uri:    u,
			client: cli,
			name:   name,
			nodeID: nodeID,
		}
		a.log.Debug("Name: %s, NodeID: %s, URI: %s", name, nodeID, u)
	}

	return nil
}

func (a *networkImpl) String() string {
	s := strings.Builder{}
	_, _ = s.WriteString("****************************************************************************************************")
	_, _ = s.WriteString("     List of nodes in the network: \n")
	_, _ = s.WriteString("  +------------------------------------------------------------------------------------------------+")
	_, _ = s.WriteString("  +  NodeID                           |     Label         |      Cluster URI                       +")
	_, _ = s.WriteString("  +------------------------------------------------------------------------------------------------+")
	for _, n := range a.nodes {
		s.WriteString(fmt.Sprintf("     %s    %s    %s", n.nodeID, n.name, n.uri))
	}
	s.WriteString("****************************************************************************************************")
	return s.String()
}
