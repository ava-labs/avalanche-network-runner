package k8s

import "net/http"

// dnsCheck is an interface used to check if a node is reachable inside kubernetes.
// We need this as long as we use plain http.Get() to check for DNS working (check according TODO),
// in order for this to be mocked
type dnsCheck interface {
	Reachable(url string) error
}

// defaultDNSChecker is the default implementation of the DNS check.
// It just uses http.Get
type defaultDNSChecker struct{}

// NewDefaultDNSChecker creates the default DNS checker
func NewDefaultDNSChecker() *defaultDNSChecker {
	return &defaultDNSChecker{}
}

// Reachable implements the dnsCheck interface
func (d *defaultDNSChecker) Reachable(url string) error {
	_, err := http.Get(url)
	return err
}
