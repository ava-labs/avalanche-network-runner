package k8s

import (
	"net/http"
)

const (
	consecutiveReachableSuccess = 5
)

var _ dnsReachableChecker = &defaultDNSReachableChecker{}

// dnsReachableChecker checks if a URL is reachable.
// We use this to check whether a K8s pod is reachable.
// We need this as long as we use plain HTTP Get calls
// to check for DNS working (there is a TODO to change this).
type dnsReachableChecker interface {
	// Returns true if we can successfully
	// make an HTTP call to [url]
	Reachable(url string) error
}

// defaultDNSReachableChecker is the default implementation of the DNS check.
// It just uses http.Get.
type defaultDNSReachableChecker struct{}

// It seems that sometimes, we are able to send a GET request to [url]
// but then an immediately subsequent HTTP request to [url] fails.
// This method only returns true if [consecutiveReachableSuccess]
// consecutive GET requests to [url] succeed, in order to ensure
// that [url] is stably reachable.
func (d *defaultDNSReachableChecker) Reachable(url string) error {
	for i := 0; i < consecutiveReachableSuccess; i++ {
		if resp, err := http.Get(url); err != nil {
			return err
		} else {
			_ = resp.Body.Close()
		}
	}
	return nil
}
