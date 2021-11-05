package helpers

import (
	"crypto/tls"
	"crypto/x509"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/hashing"
)

// GetNodeIDFromCertKey parses the node ID
// TODO add helper in AvalancheGo for this?
func GetNodeIDFromCertKey(certFile []byte, keyFile []byte) (ids.ShortID, error) {
	cert, err := tls.X509KeyPair(certFile, keyFile)
	if err != nil {
		return ids.ShortEmpty, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return ids.ShortEmpty, err
	}
	return ids.ShortID(
		hashing.ComputeHash160Array(
			hashing.ComputeHash256(cert.Leaf.Raw),
		),
	), nil
}
