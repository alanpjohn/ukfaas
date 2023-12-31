package internal

import (
	"fmt"
	"math/rand"

	networkapi "github.com/alanpjohn/uk-faas/pkg/api/network"
)

func init() {
	RegisterLoadBalancer("random", NewRandomLoadBalancer)
}

type RandomLoadBalancer struct {
	size uint64
	ips  []networkapi.IP
}

func NewRandomLoadBalancer(_ ...any) (LoadBalancer, error) {
	return &RandomLoadBalancer{
		size: 0,
		ips:  []networkapi.IP{},
	}, nil
}

func (rlb *RandomLoadBalancer) AddInstance(ip networkapi.IP) (LoadBalancer, error) {
	rlb.size += 1
	rlb.ips = append(rlb.ips, ip)
	return rlb, nil
}

func (rlb *RandomLoadBalancer) DeleteInstance(ip networkapi.IP) (LoadBalancer, error) {
	if rlb.size == 0 {
		return rlb, nil
	}
	newIpList := make([]networkapi.IP, rlb.size-1)
	index := 0
	for _, currIp := range rlb.ips {
		if ip != currIp {
			newIpList[index] = currIp
			index++
		}
	}
	return &RandomLoadBalancer{
		size: rlb.size - 1,
		ips:  newIpList,
	}, nil
}

func (rlb *RandomLoadBalancer) NextInstance() (networkapi.IP, error) {
	if rlb.size == 0 {
		return "", fmt.Errorf("no ips for instance")
	}
	index := rand.Intn(int(rlb.size))
	return rlb.ips[index], nil
}

func (rlb *RandomLoadBalancer) GetIPs() []networkapi.IP {
	return rlb.ips
}

func (rlb *RandomLoadBalancer) Size() uint64 {
	return rlb.size
}
