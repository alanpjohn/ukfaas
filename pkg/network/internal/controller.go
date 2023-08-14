package internal

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/alanpjohn/uk-faas/pkg"
	networkapi "github.com/alanpjohn/uk-faas/pkg/api/network"
)

type InternalNetworkContoller struct {
	healthCheckTable    sync.Map
	instancesMap        sync.Map
	defaultLoadBalancer LoadBalancerConstructor
	lbOpts              []any
}

func NewInternalNetworkContoller(opts ...any) (networkapi.NetworkController, error) {

	networkManager := InternalNetworkContoller{}
	for _, opt := range opts {
		ncOpt, ok := opt.(InternalNetworkContollerOption)
		if !ok {
			return &InternalNetworkContoller{}, fmt.Errorf("invalid InternalNetworkController option : %v", opt)
		}

		err := ncOpt(&networkManager)
		if err != nil {
			return &InternalNetworkContoller{}, err
		}
	}

	if networkManager.defaultLoadBalancer == nil {
		networkManager.defaultLoadBalancer = NewRandomLoadBalancer
	}

	networkManager.lbOpts = []any{}
	return &networkManager, nil
}

func (n *InternalNetworkContoller) AddServiceIP(service string, ip networkapi.IP) error {
	var (
		lb  LoadBalancer
		ok  bool
		err error
	)
	if existingLb, exists := n.instancesMap.Load(service); exists {
		lb, ok = existingLb.(LoadBalancer)
		if !ok {
			return fmt.Errorf("invalid load balancer")
		}
	} else {
		lb, err = n.defaultLoadBalancer(n.lbOpts)
		if err != nil {
			return err
		}
	}
	updatedLb, err := lb.AddInstance(ip)
	if err != nil {
		return err
	}
	n.healthCheckTable.Store(ip, true)
	n.instancesMap.Store(service, updatedLb)
	log.Printf("[InternalNetworkController.AddServiceIP] - Added IP %s to Service %s", ip, service)
	return nil
}

func (n *InternalNetworkContoller) DeleteServiceIP(service string, ip networkapi.IP) error {
	var (
		lb LoadBalancer
		ok bool
	)
	if existingLb, exists := n.instancesMap.Load(service); exists {
		lb, ok = existingLb.(LoadBalancer)
		if !ok {
			return fmt.Errorf("invalid load balancer")
		}
		updatedLb, err := lb.DeleteInstance(ip)
		if err != nil {
			return err
		}
		n.instancesMap.Store(service, updatedLb)
	}
	n.healthCheckTable.Delete(ip)
	log.Printf("[InternalNetworkController.DeleteServiceIP] - Deleted IP %s from Service %s", ip, service)
	return nil
}

func (n *InternalNetworkContoller) DeleteService(service string) error {
	var (
		lb LoadBalancer
		ok bool
	)
	if existingLb, exists := n.instancesMap.LoadAndDelete(service); exists {
		lb, ok = existingLb.(LoadBalancer)
		if !ok {
			return fmt.Errorf("invalid load balancer")
		}
		ips := lb.GetIPs()
		for ip := range ips {
			n.healthCheckTable.Delete(ip)
		}
		log.Printf("[InternalNetworkController.DeleteService] - Deleted Service %s", service)
	}

	return fmt.Errorf("service %s not found", service)
}

func (n *InternalNetworkContoller) ResolveServiceEndpoint(service string) (*url.URL, error) {
	val, exists := n.instancesMap.Load(service)
	if !exists {
		return nil, fmt.Errorf("service not found: %s", service)
	}
	lb, ok := val.(LoadBalancer)
	if !ok {
		return nil, fmt.Errorf("invalid load balancer")
	}
	var maxLen uint64 = lb.Size()
	var tries uint64
	for tries = 0; tries < maxLen || tries < 5; tries++ {
		ip, err := lb.NextInstance()
		if err != nil {
			return nil, err
		}
		if val, exists := n.healthCheckTable.Load(ip); exists {
			if isHealthy, ok := val.(bool); ok && isHealthy {
				return url.Parse(fmt.Sprintf("http://%s:%d", ip, pkg.WatchdogPort))
			}
		}
	}

	return nil, fmt.Errorf("healthy instance not found")
}

func (n *InternalNetworkContoller) AvailableIPs(service string) (uint64, error) {
	var (
		lb    LoadBalancer
		ok    bool
		count uint64
	)
	if existingLb, exists := n.instancesMap.Load(service); exists {
		lb, ok = existingLb.(LoadBalancer)
		if !ok {
			return 0, fmt.Errorf("invalid load balancer")
		}
	} else {
		return count, fmt.Errorf("service %s not found", service)
	}

	ips := lb.GetIPs()
	for _, ip := range ips {
		if val, exists := n.healthCheckTable.Load(ip); exists {
			healthy, ok := val.(bool)
			if ok && healthy {
				count++
			}
		}
	}
	return count, nil
}

func (n *InternalNetworkContoller) RunHealthChecks(ctx context.Context) {
	for {
		n.healthCheckTable.Range(func(key, value any) bool {
			select {
			case <-ctx.Done():
				return false
			default:
				ip := key.(networkapi.IP)
				health := checkHealth(ctx, ip, "/")
				n.healthCheckTable.Store(ip, health)
				time.Sleep(2 * time.Second)
			}
			return true
		})
	}
}

func checkHealth(ctx context.Context, ip networkapi.IP, healthPath string) bool {
	url := fmt.Sprintf("%s:%d%s", ip, pkg.WatchdogPort, healthPath)
	client := &http.Client{}
	req, err := http.NewRequest("GET", fmt.Sprintf("http://%s", url), nil)
	if err != nil {
		return false
	}

	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
