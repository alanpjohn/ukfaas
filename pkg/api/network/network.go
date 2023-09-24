package network

import (
	"context"
	"net/url"
)

type IP string

type NetworkServiceType string

type NetworkService interface {
	AddServiceIP(context.Context, string, IP) error
	DeleteServiceIP(context.Context, string, IP) error
	DeleteService(context.Context, string) error
	ResolveServiceEndpoint(string) (*url.URL, error)
	AvailableIPs(context.Context, string) (uint64, error)
	// RunHealthChecks(context.Context)
}
