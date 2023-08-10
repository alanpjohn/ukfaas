package network

import (
	"encoding/json"
	ioutil "io"
	"log"
	"net/http"
)

const caddyRoutesURL string = "http://localhost:2019/config/apps/http/servers/srv0/routes/1/handle/0/routes"

type routeConfig struct {
	Handle []handlerConfig  `json:"handle,omitempty"`
	Match  []pathExpression `json:"match,omitempty"`
}

type pathExpression struct {
	Path []string `json:"path,omitempty"`
}

type handlerConfig struct {
	Handler       string       `json:"handler,omitempty"`
	HealthCheck   healthChecks `json:"health_checks,omitempty"`
	LoadBalancing lbconfig     `json:"load_balancing,omitempty"`
	Upstreams     []dialconfig `json:"upstreams,omitempty"`
}

type healthChecks struct {
	Active activeHealthCheck `json:"active,omitempty"`
}

type activeHealthCheck struct {
	ExpectStatus uint16 `json:"expect_status,omitempty"`
	Interval     uint64 `json:"interval,omitempty"`
	Timeout      uint64 `json:"timeout,omitempty"`
	URI          string `json:"uri,omitempty"`
}

var defaultHealthChecks healthChecks = healthChecks{
	Active: activeHealthCheck{
		ExpectStatus: 200,
		Interval:     10000000000,
		Timeout:      30000000000,
		URI:          "/",
	},
}

type lbconfig struct {
	SelectionConfig lbSelectionPolicy `json:"selection_policy,omitempty"`
	TryDuration     uint64            `json:"try_duration,omitempty"`
}

var defaultlbConfig lbconfig = lbconfig{
	SelectionConfig: lbSelectionPolicy{
		Policy: "first",
	},
	TryDuration: 1000000000,
}

type lbSelectionPolicy struct {
	Policy string `json:"policy,omitempty"`
}

type dialconfig struct {
	Dial string `json:"dial,omitempty"`
}

func GetUKFaaSRoutes() ([]routeConfig, error) {
	response, err := http.Get(caddyRoutesURL)
	if err != nil {
		log.Println("Error sending GET request:", err)
		return nil, err
	}
	defer response.Body.Close()

	// Check if the request was successful (status code 200)
	if response.StatusCode != http.StatusOK {
		log.Println("Error: Non-OK status code received:", response.Status)
		return nil, err
	}

	// Read the response body
	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Println("Error reading response body:", err)
		return nil, err
	}

	// Print the response body as a string
	// log.Println(string(responseBody))
	var caddyroutes []routeConfig
	err = json.Unmarshal(responseBody, &caddyroutes)
	if err != nil {
		return nil, err
	}

	return caddyroutes, nil
}
