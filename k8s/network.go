package k8s

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/client"
	"github.com/ava-labs/avalanche-network-runner-local/network"
	"github.com/ava-labs/avalanche-network-runner-local/network/node"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"

	"github.com/sirupsen/logrus"

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
	timeoutDuration = 60 * time.Second
	defaultPort     = 9650
)

var errNodeDoesNotExist = errors.New("Node with given NodeID does not exist")

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
}

// Opts encapsulates kubernetes specific options
type Opts struct {
	Namespace      string // The kubernetes Namespace
	DeploymentSpec string // Identifies this network in the cluster
}

// NewAdapter creates a new adapter
func NewAdapter(opts Opts) *Adapter {
	// init k8s client
	scheme := runtime.NewScheme()
	if err := k8sapi.AddToScheme(scheme); err != nil {
		logrus.Fatal(err)
		return nil
	}
	kubeconfig := ctrl.GetConfigOrDie()
	kubeClient, err := k8scli.New(kubeconfig, k8scli.Options{Scheme: scheme})
	if err != nil {
		logrus.Fatal(err)
		return nil
	}
	logrus.Info("K8s client initialized")
	return &Adapter{
		k8scli:  kubeClient,
		opts:    opts,
		kconfig: kubeconfig,
	}
}

// NewNetwork returns a new network whose initial state is specified in the config
func (a *Adapter) NewNetwork(config network.Config) (network.Network, error) {
	a.k8sNetwork = a.createDeploymentFromConfig(config)
	if err := a.k8scli.Create(context.TODO(), a.k8sNetwork); err != nil {
		return nil, err
	}

	a.nodes = make(map[string]*K8sNode, config.NodeCount)
	a.pods = make(map[string]corev1.Pod, config.NodeCount)

	logrus.Debug("Waiting for pods to be created...")
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
	fmturi := fmt.Sprintf("http://%s:9650", testuri)
	ok := false
	for !ok {
		logrus.Debugf("checking if %s is reachable...", fmturi)
		_, err := http.Get(fmturi)
		if err == nil {
			logrus.Debugf("yes!")
			ok = true
		}
		time.Sleep(1 * time.Second)
	}

	err := a.buildIDMapping()
	if err != nil {
		return nil, err
	}

	logrus.Infof("%s\n", a)
	return a, nil
}

func (a *Adapter) GetNodesNames() []string {
	nodes := make([]string, len(a.nodes))
	i := 0
	for _, n := range a.nodes {
		nodes[i] = n.NodeID
		i++
	}
	return nodes
}

// Ready returns a channel which signals when the network is ready to be used
func (a *Adapter) Healthy() chan error {
	errCh := make(chan error)
	healthy := make(map[*K8sNode]bool)
	for _, n := range a.nodes {
		healthy[n] = false
	}

	go func() {
	OUTER:
		for {
			numHealthy := 0
			for n, h := range healthy {
				if h {
					numHealthy++
					if numHealthy == a.config.NodeCount {
						break OUTER
					}
					continue
				}
				hh, err := n.Client.HealthAPI().Health()
				if err != nil {
					logrus.Tracef("%s returned error %v", n.ShortID, err)
					continue
				}
				if hh.Healthy {
					numHealthy++
					logrus.Debugf("Node %s became healthy", n.ShortID)
					healthy[n] = true
				}
				if numHealthy == a.config.NodeCount {
					break OUTER
				}
			}
			time.Sleep(1 * time.Second)
		}
		logrus.Info("Network ready")
		errCh <- nil
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
	logrus.Info("Network cleared")
	return nil
}

// AddNode starts a new node with the config
func (a *Adapter) AddNode(cfg node.Config) (node.Node, error) {
	node := &k8sapi.Avalanchego{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Avalanchego",
			APIVersion: "chain.avax.network/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: a.opts.Namespace,
		},
		Spec: k8sapi.AvalanchegoSpec{
			DeploymentName:  "test-worker",
			BootstrapperURL: a.k8sNetwork.Status.BootstrapperURL,
			NodeCount:       1,
			Image:           a.k8sNetwork.Spec.Image,
			Tag:             a.k8sNetwork.Spec.Tag,
			Env:             a.k8sNetwork.Spec.Env,
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	if err := a.k8scli.Create(context.TODO(), node); err != nil {
		return nil, err
	}
	apiNode := &K8sNode{}
	// a.cs.CoreV1().Nodes().Create(context.TODO(), node, metav1.CreateOptions{})
	return apiNode, nil
}

// RemoveNode stops the node with this ID.
func (a *Adapter) RemoveNode(id string) error {
	if p, ok := a.pods[id]; ok {
		if err := a.cs.CoreV1().Nodes().Delete(context.TODO(), p.Name, metav1.DeleteOptions{}); err != nil {
			return err
		}
	}
	return nil
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
	for _, n := range a.nodes {
		if n.NodeID == id {
			return n, nil
		}
	}
	return nil, errNodeDoesNotExist
}

func (a *Adapter) createDeploymentFromConfig(config network.Config) *k8sapi.Avalanchego {
	// Returns a new network whose initial state is specified in the config
	newChain := &k8sapi.Avalanchego{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Avalanchego",
			APIVersion: "chain.avax.network/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.opts.DeploymentSpec,
			Namespace: a.opts.Namespace,
		},
		Spec: k8sapi.AvalanchegoSpec{
			DeploymentName: config.Name,
			NodeCount:      config.NodeCount,
			Image:          "avaplatform/avalanchego",
			Tag:            "v1.6.0",
			Env: []corev1.EnvVar{
				{
					Name:  "AVAGO_LOG_LEVEL",
					Value: config.LogLevel,
				},
			},
		},
	}

	a.config = config
	return newChain
}

func (a *Adapter) buildIDMapping() error {
	for i, u := range a.k8sNetwork.Status.NetworkMembersURI {
		logrus.Debug(u)
		cli := client.NewAPIClient(u, defaultPort, timeoutDuration)
		nid, err := cli.InfoAPI().GetNodeID()
		if err != nil {
			return err
		}
		logrus.Debug(nid)
		nodeID := fmt.Sprintf("validator-%d", i)
		shortID, err := ids.ShortFromPrefixedString(nid, constants.NodeIDPrefix)
		if err != nil {
			return fmt.Errorf("could not convert node id from string: %s", err)
		}
		a.nodes[nodeID] = &K8sNode{
			URI:     u,
			Client:  cli,
			NodeID:  nodeID,
			ShortID: shortID,
		}
		logrus.Debugf("NodeID: %s, ShortID: %s, URI: %s", nodeID, shortID, u)
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
		s.WriteString(fmt.Sprintf("     %s    %s    %s", n.ShortID, n.NodeID, n.URI))
	}
	s.WriteString("****************************************************************************************************")
	return s.String()
}
