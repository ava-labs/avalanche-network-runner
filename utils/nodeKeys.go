package utils

import (
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/ava-labs/avalanchego/staking"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"golang.org/x/sync/errgroup"
)

type EncodedNodeKeys struct {
	StakingKey  string
	StakingCert string
	BlsKey      string
}

type NodeKeys struct {
	StakingKey  []byte
	StakingCert []byte
	BlsKey      []byte
}

func EncodeNodeKeys(key *NodeKeys) *EncodedNodeKeys {
	return &EncodedNodeKeys{
		StakingKey:  string(key.StakingKey),
		StakingCert: string(key.StakingCert),
		BlsKey:      base64.StdEncoding.EncodeToString(key.BlsKey),
	}
}

func GenerateNodeKeys() (*NodeKeys, error) {
	stakingCert, stakingKey, err := staking.NewCertAndKeyBytes()
	if err != nil {
		return nil, fmt.Errorf("couldn't generate staking Cert/Key: %w", err)
	}
	key, err := bls.NewSecretKey()
	if err != nil {
		return nil, fmt.Errorf("couldn't generate new signing key: %w", err)
	}
	return &NodeKeys{
		StakingKey:  stakingKey,
		StakingCert: stakingCert,
		BlsKey:      bls.SecretKeyToBytes(key),
	}, nil
}

func GenerateNNodeKeys(num int) ([]*NodeKeys, error) {
	nodesKeys := []*NodeKeys{}
	lock := sync.Mutex{}
	eg := errgroup.Group{}
	for i := 0; i < num; i++ {
		eg.Go(func() error {
			keys, err := GenerateNodeKeys()
			if err != nil {
				return err
			}
			lock.Lock()
			nodesKeys = append(nodesKeys, keys)
			lock.Unlock()
			return nil
		})
	}
	err := eg.Wait()
	return nodesKeys, err
}
