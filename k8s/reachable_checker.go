package k8s

import (
	"context"
	"net/http"
	"time"
)

const (
	consecutiveReachableSuccess = 5
	reachableCheckFreq          = time.Second
)

var _ dnsReachableChecker = &defaultDNSReachableChecker{}

// dnsReachableChecker checks if a URL is reachable.
// We use this to check whether a K8s pod is reachable.
// We need this as long as we use plain HTTP Get calls
// to check for DNS working (there is a TODO to change this).
type dnsReachableChecker interface {
	// Returns true if we can successfully
	// make an HTTP call to [url]
	Reachable(ctx context.Context, url string) bool
}

// defaultDNSReachableChecker is the default implementation of the DNS check.
// It just uses http.Get.
type defaultDNSReachableChecker struct{}

// It seems that sometimes, we are able to send a GET request to [url]
// but then an immediately subsequent HTTP request to [url] fails.
// This method only returns true if [consecutiveReachableSuccess]
// consecutive GET requests to [url], with [reachableCheckFreq]
// between each check, succeed, in order to ensure that [url] is stably reachable.
func (d *defaultDNSReachableChecker) Reachable(ctx context.Context, url string) bool {
	for i := 0; i < consecutiveReachableSuccess; i++ {
		if resp, err := http.Get(url); err != nil {
			return false
		} else {
			_ = resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(reachableCheckFreq): // Wait
		}
	}
	return true
}
