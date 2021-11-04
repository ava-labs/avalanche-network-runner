package k8s

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/api"
	"github.com/ava-labs/avalanche-network-runner-local/constants"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
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

var (
	errNodeDoesNotExist = errors.New("Node with given NodeID does not exist")
	errStopped          = errors.New("network stopped")
)

// Adapter is the kubernetes data type representing a network adapter.
// It implements the network.Network interface
type Adapter struct {
	config     network.Config
	k8sNetwork *k8sapi.Avalanchego
	k8scli     k8scli.Client
	opts       Opts
	kconfig    *rest.Config
	cs         *kubernetes.Clientset
	nodes      map[string]*K8sNode
	pods       map[string]corev1.Pod
	// Closed when network is done shutting down
	closedOnStopCh chan struct{}
	log            logging.Logger
}

// Opts encapsulates kubernetes specific options
type Opts struct {
	Namespace      string // The kubernetes Namespace
	DeploymentSpec string // Identifies this network in the cluster
	Kind           string // Identifies the object Kind for the operator
	APIVersion     string // The APIVersion of the kubernetes object
	Image          string // The docker image to use
	Tag            string // The docker tag to use
	LogLevelKey    string // The key for the log level value
	Log            logging.Logger
}

// NewAdapter creates a new adapter
func NewAdapter(opts Opts) *Adapter {
	// init k8s client
	log := opts.Log
	scheme := runtime.NewScheme()
	if err := k8sapi.AddToScheme(scheme); err != nil {
		log.Fatal("%v", err)
		return nil
	}
	kubeconfig := ctrl.GetConfigOrDie()
	kubeClient, err := k8scli.New(kubeconfig, k8scli.Options{Scheme: scheme})
	if err != nil {
		log.Fatal("%v", err)
		return nil
	}
	opts.Log.Info("K8s client initialized")
	return &Adapter{
		k8scli:         kubeClient,
		opts:           opts,
		kconfig:        kubeconfig,
		closedOnStopCh: make(chan struct{}),
		log:            log,
	}
}

// NewNetwork returns a new network whose initial state is specified in the config
func (a *Adapter) NewNetwork(config network.Config) (network.Network, error) {
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
			Namespace: a.opts.Namespace,
		}, a.k8sNetwork)
		if err != nil {
			return nil, err
		}
		time.Sleep(1 * time.Second)
	}

	// use the last uri to try connecting to it until it resolves
	// otherwise we have to sleep indiscriminately, we can't just use the API right away:
	// the kubernetes cluster has already created the pods but not the DNS names,
	// so using the API Client too early results in an error.
	testuri := a.k8sNetwork.Status.NetworkMembersURI[config.NodeCount-1]
	fmturi := fmt.Sprintf("http://%s:%d", testuri, constants.DefaultPort)
	ok := false
	for !ok {
		a.log.Debug("checking if %s is reachable...", fmturi)
		_, err := http.Get(fmturi)
		if err == nil {
			a.log.Debug("%s has become reachable", fmturi)
			ok = true
		}
		time.Sleep(1 * time.Second)
	}

	// build a mapping from the given k8s URIs to names/ids
	err := a.buildNodeMapping()
	if err != nil {
		return nil, err
	}

	a.log.Info("%s\n", a)
	return a, nil
}

// GetNodesNames returns an array of node names
func (a *Adapter) GetNodesNames() []string {
	nodes := make([]string, len(a.nodes))
	i := 0
	for _, n := range a.nodes {
		nodes[i] = n.nodeID
		i++
	}
	return nodes
}

// Healthy returns a channel which signals when the network is ready to be used
func (a *Adapter) Healthy() chan error {
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
func (a *Adapter) Stop(ctx context.Context) error {
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
func (a *Adapter) AddNode(cfg node.Config) (node.Node, error) {
	node := &k8sapi.Avalanchego{
		TypeMeta: metav1.TypeMeta{
			Kind:       a.opts.Kind,
			APIVersion: a.opts.APIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: a.opts.Namespace,
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
					corev1.ResourceCPU:    resource.MustParse(constants.K8sResourceLimitsCPU), // TODO: Should these be supplied by Opts rather than const?
					corev1.ResourceMemory: resource.MustParse(constants.K8sResourceLimitsMemory),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(constants.K8sResourceRequestCPU),
					corev1.ResourceMemory: resource.MustParse(constants.K8sResourceRequestMemory),
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
func (a *Adapter) RemoveNode(id string) error {
	if p, ok := a.pods[id]; ok {
		if err := a.cs.CoreV1().Nodes().Delete(context.TODO(), p.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
		return nil
	}
	return errNodeDoesNotExist
}

// GetAllNodes returns all nodes
func (a *Adapter) GetAllNodes() []node.Node {
	nodes := make([]node.Node, len(a.nodes))
	i := 0
	for _, n := range a.nodes {
		nodes[i] = n
		i++
	}
	return nodes
}

// GetNode returns the node with this ID.
func (a *Adapter) GetNode(id string) (node.Node, error) {
	if n, ok := a.nodes[id]; ok {
		return n, nil
	}
	return nil, errNodeDoesNotExist
}

func (a *Adapter) createDeploymentFromConfig() *k8sapi.Avalanchego {
	// Returns a new network whose initial state is specified in the config
	newChain := &k8sapi.Avalanchego{
		TypeMeta: metav1.TypeMeta{
			Kind:       a.opts.Kind,
			APIVersion: a.opts.APIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.opts.DeploymentSpec,
			Namespace: a.opts.Namespace,
		},
		Spec: k8sapi.AvalanchegoSpec{
			DeploymentName: a.config.Name,
			NodeCount:      a.config.NodeCount,
			Image:          a.opts.Image,
			Tag:            a.opts.Tag,
			Env: []corev1.EnvVar{
				{
					Name:  a.opts.LogLevelKey,
					Value: a.config.LogLevel,
				},
			},
		},
	}

	return newChain
}

// buildNodeMapping creates the actual k8s node representation and creates the nodes map
// with the name as the key
func (a *Adapter) buildNodeMapping() error {
	for i, u := range a.k8sNetwork.Status.NetworkMembersURI {
		a.log.Debug(u)
		cli := api.NewAPIClient(u, constants.DefaultPort, constants.TimeoutDuration)
		nid, err := cli.InfoAPI().GetNodeID()
		if err != nil {
			return err
		}
		a.log.Debug(nid)
		nodeID := fmt.Sprintf("validator-%d", i)
		shortID, err := ids.ShortFromPrefixedString(nid, avagoconst.NodeIDPrefix)
		if err != nil {
			return fmt.Errorf("could not convert node id from string: %s", err)
		}
		a.nodes[nodeID] = &K8sNode{
			uri:     u,
			client:  cli,
			nodeID:  nodeID,
			shortID: shortID,
		}
		a.log.Debug("NodeID: %s, ShortID: %s, URI: %s", nodeID, shortID, u)
	}

	return nil
}

func (a *Adapter) String() string {
	s := strings.Builder{}
	_, _ = s.WriteString("****************************************************************************************************")
	_, _ = s.WriteString("     List of nodes in the network: \n")
	_, _ = s.WriteString("  +------------------------------------------------------------------------------------------------+")
	_, _ = s.WriteString("  +  NodeID                           |     Label         |      Cluster URI                       +")
	_, _ = s.WriteString("  +------------------------------------------------------------------------------------------------+")
	for _, n := range a.nodes {
		s.WriteString(fmt.Sprintf("     %s    %s    %s", n.shortID, n.nodeID, n.uri))
	}
	s.WriteString("****************************************************************************************************")
	return s.String()
}
