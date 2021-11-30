package k8s

import "net/http"

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

func (d *defaultDNSReachableChecker) Reachable(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
