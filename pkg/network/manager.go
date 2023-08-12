package network

import (
	"fmt"

	networkapi "github.com/alanpjohn/uk-faas/pkg/api/network"
	"github.com/alanpjohn/uk-faas/pkg/network/caddy"
	// "github.com/alanpjohn/uk-faas/pkg/network/internal"
)

func init() {
	networkControllers = map[NetworkControllerType]NetworkControllerConstructor{
		// "internal": internal.NewInternalNetworkContoller,
		"caddy": caddy.NewCaddyController,
	}
}

type NetworkControllerType string

type NetworkControllerConstructor func(...any) (networkapi.NetworkController, error)

var networkControllers map[NetworkControllerType]NetworkControllerConstructor

func RegisterNetworkController(ncType NetworkControllerType, ncConstructor NetworkControllerConstructor) {
	networkControllers[ncType] = ncConstructor
}

func GetNetworkController(ncType NetworkControllerType, options ...any) (networkapi.NetworkController, error) {
	if ncConstruct, exists := networkControllers[ncType]; exists {
		return ncConstruct(options...)
	}

	return nil, fmt.Errorf("network Controller %s not found", ncType)
}
