package network

import "net/url"

type NetworkController interface {
	AddFunctionInstance(string, string) error
	DeleteFunction(string) error
	DeleteFunctionInstance(string, string) error
	GetServiceURl(string) (*url.URL, error)
	HealthyInstances(string) (uint64, error)
}
