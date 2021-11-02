package k8s

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner-local/api"
	"github.com/ava-labs/avalanche-network-runner-local/client"
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
// It implements the api.Network interface
type Adapter struct {
	k8sNetwork *k8sapi.Avalanchego
	k8scli     k8scli.Client
	opts       Opts
	config     *api.NetworkConfig
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
func (a *Adapter) NewNetwork(config *api.NetworkConfig) (api.Network, error) {
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

	// get deployed pods
	var err error
	a.cs, err = kubernetes.NewForConfig(a.kconfig)
	if err != nil {
		return nil, err
	}

	pods, err := a.cs.CoreV1().Pods(a.opts.Namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, p := range pods.Items {
		name := p.GetName()
		id := name[len("avago-"):] // TODO: constant and/or extract from some env var
		a.pods[id] = p
	}

	if err := a.waitForPodsRunning(); err != nil {
		return nil, err
	}

	err = a.buildIDMapping()
	if err != nil {
		return nil, err
	}

	a.printNetwork()
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
	errc := make(chan error)
	healthy := make(map[*K8sNode]bool)
	for _, n := range a.nodes {
		healthy[n] = false
	}

	go func() {
	OUTER:
		for {
			hcnt := 0
			for n, h := range healthy {
				if h {
					hcnt++
					if hcnt == a.config.NodeCount {
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
					hcnt++
					logrus.Debugf("Node %s became healthy", n.ShortID)
					healthy[n] = true
				}
				if hcnt == a.config.NodeCount {
					break OUTER
				}
			}
			time.Sleep(1 * time.Second)
		}
		logrus.Info("Network ready")
		errc <- nil
	}()
	return errc
}

// Stop all the nodes
func (a *Adapter) Stop(ctx context.Context) error {
	// delete network
	err := a.k8scli.Delete(ctx, a.k8sNetwork)
	if err != nil {
		logrus.Fatal(err)
		return err
	}
	logrus.Info("Network cleared")
	return nil
}

// AddNode starts a new node with the config
func (a *Adapter) AddNode(cfg api.NodeConfig) (api.Node, error) {
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
func (a *Adapter) GetAllNodes() []api.Node {
	nodes := make([]api.Node, len(a.nodes))
	i := 0
	for _, n := range a.nodes {
		nodes[i] = n
		i++
	}
	return nodes
}

// GetNode returns the node with this ID.
func (a *Adapter) GetNode(id string) (api.Node, error) {
	for _, n := range a.nodes {
		if n.NodeID == id {
			return n, nil
		}
	}

	return nil, errNodeDoesNotExist
}

func (a *Adapter) createDeploymentFromConfig(config *api.NetworkConfig) *k8sapi.Avalanchego {
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

func (a *Adapter) waitForPodsRunning() error {
	var wg sync.WaitGroup

	if len(a.pods) < a.config.NodeCount {
		return errors.New("not all expected pods constructed")
	}

	for _, p := range a.pods {
		wg.Add(1)
		go a.waitForSinglePodRunning(p, &wg)
	}
	logrus.Info("Waiting for pods to be running...")
	wg.Wait()
	logrus.Info("Pods running")
	return nil
}

func (a *Adapter) waitForSinglePodRunning(p corev1.Pod, wg *sync.WaitGroup) {
	for {
		pod, err := a.cs.CoreV1().Pods(p.Namespace).Get(context.TODO(), p.Name, metav1.GetOptions{})
		if err != nil {
			logrus.Errorf("Error querying pod status: %v", err)
		}

		switch pod.Status.Phase {
		case corev1.PodRunning:
			logrus.Debugf("Pod %s is running", pod.Name)
			wg.Done()
			return
		default:
			continue
		}
	}
}

func (a *Adapter) printNetwork() {
	logrus.Info("****************************************************************************************************")
	logrus.Info("     List of nodes in the network: \n")
	logrus.Info("  +------------------------------------------------------------------------------------------------+")
	logrus.Info("  +  NodeID                           |     Label         |      Cluster URI                       +")
	logrus.Info("  +------------------------------------------------------------------------------------------------+")
	for _, n := range a.nodes {
		logrus.Infof("     %s    %s    %s", n.ShortID, n.NodeID, n.URI)
	}
	logrus.Info("****************************************************************************************************")
}
