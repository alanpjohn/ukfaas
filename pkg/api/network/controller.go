package network

import (
	"context"
	"net/url"
)

type NetworkController interface {
	AddServiceIP(string, IP) error
	DeleteServiceIP(string, IP) error
	DeleteService(string) error
	ResolveServiceEndpoint(string) (*url.URL, error)
	AvailableIPs(string) (uint64, error)
	RunHealthChecks(context.Context)
}
