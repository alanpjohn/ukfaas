package loadbalancer

type InternalNetworkServiceOption func(*InternalNetworkService) error

func WithLoadBalancer(loadbalancerType LoadBalancerType) InternalNetworkServiceOption {
	return func(nm *InternalNetworkService) (err error) {
		nm.defaultLoadBalancer = loadBalancers[loadbalancerType]
		return
	}
}
