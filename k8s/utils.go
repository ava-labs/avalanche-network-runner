package k8s

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/utils"
	k8sapi "github.com/ava-labs/avalanchego-operator/api/v1alpha1"
	"github.com/ava-labs/avalanchego/config"
	"github.com/ava-labs/avalanchego/utils/logging"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

// Given a node's config and genesis, returns the environment
// variables (i.e. config flags) to give to the node
func buildNodeEnv(log logging.Logger, genesis []byte, c node.Config) ([]corev1.EnvVar, error) {
	conf := map[string]interface{}{}

	// Parse config from file if one given
	if c.ConfigFile != "" {
		if err := json.Unmarshal([]byte(c.ConfigFile), &conf); err != nil {
			return nil, err
		}
	}

	// If a key is in the config file and flags, the flag takes precedence.
	for key, val := range c.Flags {
		if _, ok := conf[key]; ok {
			log.Info("overwriting key %s in config file with value given in flag", key)
		}
		conf[key] = val
	}

	// Parse network ID from genesis
	networkID, err := utils.NetworkIDFromGenesis(genesis)
	if err != nil {
		return nil, err
	}

	// Make sure network ID in config file / flag, if given,
	// matches genesis network ID
	if gotNetworkID, ok := conf[config.NetworkNameKey]; ok {
		if gotNetworkID, ok := gotNetworkID.(float64); ok && uint32(gotNetworkID) != networkID {
			return nil, fmt.Errorf(
				"network ID in config file / flag (%d) != network ID in genesis (%d)",
				uint32(gotNetworkID), networkID,
			)
		}
	}

	// For each config key, convert it to the format
	// AvalancheGo expects environment variable config keys in.
	// e.g. bootstrap-ips --> AVAGO_BOOTSTRAP_IPS
	// e.g. log-level --> AVAGO_LOG_LEVEL
	env := []corev1.EnvVar{
		{
			// Provide environment variable giving the network ID
			Name:  convertKey(config.NetworkNameKey),
			Value: fmt.Sprint(networkID),
		},
	}
	for key, val := range conf {
		env = append(env, corev1.EnvVar{
			Name:  convertKey(key),
			Value: fmt.Sprintf("%v", val),
		})
	}
	return env, nil
}

// Takes a node's config and genesis and returns the node as a k8s object spec
func buildK8sObjSpec(log logging.Logger, genesis []byte, c node.Config) (*k8sapi.Avalanchego, error) {
	env, err := buildNodeEnv(log, genesis, c)
	if err != nil {
		return nil, err
	}
	certs := []k8sapi.Certificate{
		{
			Cert: base64.StdEncoding.EncodeToString([]byte(c.StakingCert)),
			Key:  base64.StdEncoding.EncodeToString([]byte(c.StakingKey)),
		},
	}
	var k8sConf ObjectSpec
	if err := json.Unmarshal(c.ImplSpecificConfig, &k8sConf); err != nil {
		return nil, fmt.Errorf("Unmarshalling an expected k8s.ObjectSpec failed: %w", err)
	}

	if err := validateObjectSpec(k8sConf); err != nil {
		return nil, err
	}

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
			DeploymentName:  k8sConf.Identifier,
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

// Validates an ObjectSpec.
// The tag value can be empty so not checked.
func validateObjectSpec(k8sobj ObjectSpec) error {
	switch {
	case k8sobj.Identifier == "":
		return errors.New("name should not be empty")
	case k8sobj.APIVersion == "":
		return errors.New("APIVersion should not be empty")
	case k8sobj.Kind != "Avalanchego":
		// only "AvalancheGo" currently supported -- mandated by avalanchego-operator
		return fmt.Errorf("expected \"Avalanchego\" but got %q", k8sobj.Kind)
	case k8sobj.Namespace == "":
		return errors.New("namespace should be defined to avoid unintended consequences")
	case k8sobj.Image == "" || strings.Index(k8sobj.Image, "/") == 1:
		return fmt.Errorf("image string %q is invalid, it can't be empty and must contain a %q to describe a valid image repo", k8sobj.Image, "/")
	default:
		return nil
	}
}

// Takes the genesis of a network and node configs and returns:
// 1) The beacon nodes
// 2) The non-beacon nodes
// as avalanchego-operator compatible descriptions.
// May return nil slices.
func createDeploymentFromConfig(params networkParams) ([]*k8sapi.Avalanchego, []*k8sapi.Avalanchego, error) {
	// Give each flag in the network config to each node's config.
	// If a flag is defined in both the network config and the node config,
	// the value given in the node config takes precedence.
	for flagName, flagVal := range params.conf.Flags {
		for i := range params.conf.NodeConfigs {
			nodeConfig := &params.conf.NodeConfigs[i]
			if len(nodeConfig.Flags) == 0 {
				nodeConfig.Flags = make(map[string]interface{})
			}
			// Do not overwrite flags described in the nodeConfig
			if val, ok := nodeConfig.Flags[flagName]; !ok {
				nodeConfig.Flags[flagName] = flagVal
			} else {
				params.log.Info(
					"not overwriting node config flag %s (value %v) with network config flag (value %v)",
					flagName, val, flagVal,
				)
			}
		}
	}

	var beacons, nonBeacons []*k8sapi.Avalanchego
	names := make(map[string]struct{})
	for _, nodeConfig := range params.conf.NodeConfigs {
		spec, err := buildK8sObjSpec(params.log, []byte(params.conf.Genesis), nodeConfig)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := names[spec.Name]; exists {
			return nil, nil, fmt.Errorf("node with name name %q already exists", spec.Name)
		}
		names[spec.Name] = struct{}{}
		if nodeConfig.IsBeacon {
			beacons = append(beacons, spec)
		} else {
			nonBeacons = append(nonBeacons, spec)
		}
	}
	return beacons, nonBeacons, nil
}
