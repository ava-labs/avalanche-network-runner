package utils

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/hashing"
)

const genesisNetworkIDKey = "networkID"

func ToNodeID(stakingKey, stakingCert []byte) (ids.ShortID, error) {
	cert, err := tls.X509KeyPair(stakingCert, stakingKey)
	if err != nil {
		return ids.ShortID{}, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return ids.ShortID{}, err
	}
	nodeID := ids.ShortID(
		hashing.ComputeHash160Array(
			hashing.ComputeHash256(cert.Leaf.Raw),
		),
	)
	return nodeID, err
}

// Returns the network ID in the given genesis
func NetworkIDFromGenesis(genesis []byte) (uint32, error) {
	genesisMap := map[string]interface{}{}
	if err := json.Unmarshal(genesis, &genesisMap); err != nil {
		return 0, fmt.Errorf("couldn't unmarshal genesis: %w", err)
	}
	networkIDIntf, ok := genesisMap[genesisNetworkIDKey]
	if !ok {
		return 0, fmt.Errorf("couldn't find key %q in genesis", genesisNetworkIDKey)
	}
	networkID, ok := networkIDIntf.(float64)
	if !ok {
		return 0, fmt.Errorf("expected float64 but got %T", networkIDIntf)
	}
	return uint32(networkID), nil
}

type zeroReader struct {
}

// Read replaces the contents of dst with zeros.
func (zr *zeroReader) Read(dst []byte) (n int, err error) {
	for i := range dst {
		dst[i] = 0
	}
	return len(dst), nil
}

// Creates a new staking private key / staking certificate pair.
// Deterministically based on a given Reader
// Returns the PEM byte representations of both.
func NewDeterministicCertAndKeyBytes(random io.Reader) ([]byte, []byte, error) {

	// Create key to sign cert
	// Both RSA and ECDSA use a non deterministic Reader to change the state
	// of the deterministic one. But RSA golang implementation does it before key
	// generation, is not useful
	key, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't generate ecdsa key: %w", err)
	}

	// Create self-signed staking cert
	certTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(0),
		NotBefore:             time.Date(2000, time.January, 0, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(3000, time.January, 0, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageDataEncipherment,
		BasicConstraintsValid: true,
	}
	// a zeroReader is used to avoid ECDSA signing to change the state of the deterministic reader
	certBytes, err := x509.CreateCertificate(zeroReader{}, certTemplate, certTemplate, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't create certificate: %w", err)
	}
	var certBuff bytes.Buffer
	if err := pem.Encode(&certBuff, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes}); err != nil {
		return nil, nil, fmt.Errorf("couldn't write cert file: %w", err)
	}

	privBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't marshal private key: %w", err)
	}

	var keyBuff bytes.Buffer
	if err := pem.Encode(&keyBuff, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return nil, nil, fmt.Errorf("couldn't write private key: %w", err)
	}

	return certBuff.Bytes(), keyBuff.Bytes(), nil
}
