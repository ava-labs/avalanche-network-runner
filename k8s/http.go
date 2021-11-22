package k8s

import "net/http"

type dnsCheck interface {
	Reachable(url string) error
}

type DefaultDNSChecker struct{}

func NewDefaultDNSChecker() *DefaultDNSChecker {
	return &DefaultDNSChecker{}
}

func (d *DefaultDNSChecker) Reachable(url string) error {
	var err error
	if _, err = http.Get(url); err == nil {
		return nil
	}
	return err
}
