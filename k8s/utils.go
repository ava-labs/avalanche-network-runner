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
func buildNodeEnv(genesis []byte, c node.Config) ([]corev1.EnvVar, error) {
	if c.ConfigFile == "" {
		return []corev1.EnvVar{}, nil
	}
	var avagoConf map[string]interface{} // AvalancheGo config file as a map
	if err := json.Unmarshal([]byte(c.ConfigFile), &avagoConf); err != nil {
		return nil, err
	}
	networkID, err := utils.NetworkIDFromGenesis(genesis)
	if err != nil {
		return nil, err
	}

	// For each config flag, convert it to the format
	// AvalancheGo expects environment variable config flags in.
	// e.g. bootstrap-ips --> AVAGO_BOOTSTRAP_IPS
	// e.g. log-level --> AVAGO_LOG_LEVEL
	env := make([]corev1.EnvVar, 0, len(avagoConf)+1)
	addedKeys := make(map[string]*corev1.EnvVar)
	for key, val := range avagoConf {
		// we use the network id from genesis -- ignore the one in config
		if key == config.NetworkNameKey {
			// We just override the network ID with the one from genesis after the iteration
			continue
		}
		v := corev1.EnvVar{
			Name:  convertKey(key),
			Value: val.(string),
		}
		env = append(env, v)
		addedKeys[key] = &v
	}
	// Add flags as env vars to the node
	// If a flag has already been set via config file,
	// override it
	for key, val := range c.Flags {
		// we use the network id from genesis -- ignore the one in flags
		if key == config.NetworkNameKey {
			// We just override the network ID with the one from genesis after the iteration
			continue
		}
		// override this one
		if envVar, ok := addedKeys[key]; ok {
			*envVar = corev1.EnvVar{
				Name:  convertKey(key),
				Value: val.(string),
			}
		} else {
			// new var
			v := corev1.EnvVar{
				Name:  convertKey(key),
				Value: val.(string),
			}
			env = append(env, v)
		}
	}
	// Provide environment variable giving the network ID
	v := corev1.EnvVar{
		Name:  convertKey(config.NetworkNameKey),
		Value: fmt.Sprint(networkID),
	}
	env = append(env, v)
	return env, nil
}

// Takes a node's config and genesis and returns the node as a k8s object spec
func buildK8sObjSpec(genesis []byte, c node.Config) (*k8sapi.Avalanchego, error) {
	env, err := buildNodeEnv(genesis, c)
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
	var beacons, nonBeacons []*k8sapi.Avalanchego
	names := make(map[string]struct{})

	for flagName, flagVal := range params.conf.Flags {
		for i := range params.conf.NodeConfigs {
			nodeConfig := &params.conf.NodeConfigs[i]
			if len(nodeConfig.Flags) == 0 {
				nodeConfig.Flags = make(map[string]interface{})
			}
			// Do not overwrite flags described in the nodeConfig
			if _, ok := nodeConfig.Flags[flagName]; !ok {
				nodeConfig.Flags[flagName] = flagVal
			} else {
				params.log.Debug("found same flag %s in node config - skipping overwrite", flagName)
			}
		}
	}
	for _, c := range params.conf.NodeConfigs {
		spec, err := buildK8sObjSpec([]byte(params.conf.Genesis), c)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := names[spec.Name]; exists {
			return nil, nil, fmt.Errorf("node with name name %q already exists", spec.Name)
		}
		names[spec.Name] = struct{}{}
		if c.IsBeacon {
			beacons = append(beacons, spec)
		} else {
			nonBeacons = append(nonBeacons, spec)
		}
	}
	return beacons, nonBeacons, nil
}
