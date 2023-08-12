package internal

import networkapi "github.com/alanpjohn/uk-faas/pkg/api/network"

type LoadBalancer interface {
	AddInstance(networkapi.IP) (LoadBalancer, error)
	DeleteInstance(networkapi.IP) (LoadBalancer, error)
	NextInstance() (networkapi.IP, error)
	GetIPs() []networkapi.IP
	Size() uint64
}

type LoadBalancerType string

type LoadBalancerConstructor func(...any) (LoadBalancer, error)

var loadBalancers map[LoadBalancerType]LoadBalancerConstructor = map[LoadBalancerType]LoadBalancerConstructor{}

func RegisterLoadBalancer(lbType LoadBalancerType, lbConstructor LoadBalancerConstructor) {
	loadBalancers[lbType] = lbConstructor
}
