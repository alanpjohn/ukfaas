package network

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"encoding/json"

	"github.com/alanpjohn/uk-faas/pkg"
)

type CaddyController struct {
	sync.RWMutex
	serviceIPCache  map[string][]string
	permanentRoutes []routeConfig
}

func NewCaddyController() (*CaddyController, error) {
	routes, err := GetUKFaaSRoutes()
	if err != nil {
		return nil, err
	}

	serviceIPCache := make(map[string][]string)

	log.Println("[CaddyController] Setup Caddy Controller")
	return &CaddyController{
		// routes:         routes,
		serviceIPCache:  serviceIPCache,
		permanentRoutes: routes,
	}, nil
}

func (c *CaddyController) AddFunctionInstance(service string, ipaddr string) error {
	c.Lock()
	defer c.Unlock()

	uri := fmt.Sprintf("%s:%d", ipaddr, pkg.WatchdogPort)
	if _, exists := c.serviceIPCache[service]; !exists {
		log.Printf("[CaddyController] adding IP %s to existing service %s\n", uri, service)
		c.serviceIPCache[service] = []string{uri}
		return c.reloadConfig()
	}

	log.Printf("[CaddyController] adding IP %s to new service %s\n", uri, service)
	ips := c.serviceIPCache[service]
	ips = append(ips, uri)
	c.serviceIPCache[service] = ips

	return c.reloadConfig()
}

func (c *CaddyController) DeleteFunction(service string) error {
	c.Lock()
	defer c.Unlock()

	if _, exists := c.serviceIPCache[service]; !exists {
		return fmt.Errorf("not found")
	}

	log.Printf("[CaddyController] deleting service: %s\n", service)
	delete(c.serviceIPCache, service)

	return c.reloadConfig()
}

func (c *CaddyController) DeleteFunctionInstance(service string, ipaddr string) error {
	c.Lock()
	defer c.Unlock()

	if _, exists := c.serviceIPCache[service]; !exists {
		return fmt.Errorf("not found")
	}

	log.Printf("[CaddyController] deleting IP %s from existing service %s\n", ipaddr, service)
	var ips []string
	for _, ip := range c.serviceIPCache[service] {
		if strings.Split(ip, ":")[0] != ipaddr {
			ips = append(ips, ip)
		}
	}

	if len(ips) == 0 {
		delete(c.serviceIPCache, service)
	} else {
		c.serviceIPCache[service] = ips
	}

	return c.reloadConfig()
}

func (c *CaddyController) getConfig() ([]routeConfig, error) {
	updateRoutes := c.permanentRoutes
	for service, ips := range c.serviceIPCache {
		handle := handlerConfig{
			Handler:       "reverse_proxy",
			HealthCheck:   defaultHealthChecks,
			LoadBalancing: defaultlbConfig,
			Upstreams:     []dialconfig{},
		}
		for _, ip := range ips {
			dial := dialconfig{
				Dial: ip,
			}
			handle.Upstreams = append(handle.Upstreams, dial)
		}

		pExpr := pathExpression{
			Path: []string{ServiceToURL(service)},
		}

		updateRoutes = append(updateRoutes, routeConfig{
			Handle: []handlerConfig{handle},
			Match:  []pathExpression{pExpr},
		})
	}
	return updateRoutes, nil
}

func (c *CaddyController) reloadConfig() error {
	updateRoutes, err := c.getConfig()
	if err != nil {
		return fmt.Errorf("something went wrong")
	}

	// log.Println(updateRoutes)
	rawBody, err := json.Marshal(updateRoutes)
	if err != nil {
		return err
	}
	log.Printf("[CaddyController] - Debug New Config: %s\n", rawBody)

	req, err := http.NewRequest(http.MethodPatch, caddyRoutesURL, bytes.NewReader(rawBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	log.Printf("[CaddyController] Reload Caddy: %s\n", resp.Body)
	defer resp.Body.Close()
	return nil
}

func (c *CaddyController) GetServiceURl(service string) (*url.URL, error) {
	c.RLock()
	defer c.RUnlock()

	if _, exists := c.serviceIPCache[service]; exists {
		return url.Parse(fmt.Sprintf("http://localhost:%d%s", pkg.GatewayPort, ServiceToURL(service)))
	}

	return &url.URL{}, fmt.Errorf("service not found : %s", service)
}
