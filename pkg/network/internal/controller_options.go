package internal

type InternalNetworkContollerOption func(*InternalNetworkContoller) error

func WithLoadBalancer(loadbalancerType LoadBalancerType) InternalNetworkContollerOption {
	return func(nm *InternalNetworkContoller) (err error) {
		nm.defaultLoadBalancer = loadBalancers[loadbalancerType]
		return
	}
}
