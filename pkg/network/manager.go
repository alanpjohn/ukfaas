package network

import (
	"context"
	"fmt"

	networkapi "github.com/alanpjohn/uk-faas/pkg/api/network"
)

type NetworkServiceConstructor func(context.Context, ...any) (networkapi.NetworkService, error)

var (
	networkServiceOpts         = make(map[networkapi.NetworkServiceType][]any)
	networkServiceConstructors = make(map[networkapi.NetworkServiceType]NetworkServiceConstructor)
)

func RegisterNetworkService(networkServiceType networkapi.NetworkServiceType, constructor NetworkServiceConstructor, opts ...any) {
	networkServiceConstructors[networkServiceType] = constructor
	networkServiceOpts[networkServiceType] = opts
}

func GetNetworkService(ctx context.Context, networkServiceType networkapi.NetworkServiceType) (networkapi.NetworkService, error) {
	constructor, ok := networkServiceConstructors[networkServiceType]
	if !ok {
		return nil, fmt.Errorf("no NetworkService for %s specified", networkServiceType)
	}
	opts, ok := networkServiceOpts[networkServiceType]
	if !ok {
		return constructor(ctx)
	}
	return constructor(ctx, opts)
}
