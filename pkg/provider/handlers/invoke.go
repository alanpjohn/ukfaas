package handlers

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/alanpjohn/uk-faas/pkg"
	functionapi "github.com/alanpjohn/uk-faas/pkg/api/function"
	machineapi "github.com/alanpjohn/uk-faas/pkg/api/machine"
	networkapi "github.com/alanpjohn/uk-faas/pkg/api/network"
)

type InvokeResolver struct {
	fStore       functionapi.FunctionService
	mStore       machineapi.MachineService
	networkStore networkapi.NetworkService
}

func NewInvokeResolver(f functionapi.FunctionService, m machineapi.MachineService) *InvokeResolver {
	return &InvokeResolver{
		fStore:       f,
		mStore:       m,
		networkStore: m.NetworkService(),
	}
}

func (i *InvokeResolver) Resolve(functionName string) (url.URL, error) {
	ctx := context.Background()
	if strings.Contains(functionName, ".") {
		functionName = strings.TrimSuffix(functionName, "."+pkg.DefaultFunctionNamespace)
	}
	actualFunctionName := functionName
	if function, err := i.fStore.GetFunction(ctx, actualFunctionName); err == nil {
		if i.mStore.GetReplicas(ctx, actualFunctionName) == 0 {
			log.Printf("[InvokeResolver.Resolve] - Scaling instances to 1 for %s", actualFunctionName)
			ctx := context.Background()
			scaleErr := i.mStore.NewMachine(ctx, function)
			if scaleErr != nil {
				log.Printf("[Resolve] - Error %v\n", err)
				return url.URL{}, scaleErr
			}
		}
		urlRes, err := i.networkStore.ResolveServiceEndpoint(actualFunctionName)
		if err != nil {
			log.Printf("[Resolve] - Error %v\n", err)
			return url.URL{}, err
		}
		log.Printf("[InvokeResolver.Resolve] - Resolved %s to %v\n", actualFunctionName, urlRes)
		// log.Printf("[Resolve] : Resolved %s to %s\n", functionName, urlRes)
		return *urlRes, nil
	} else {
		log.Printf("[Resolve] - Error %v\n", err)
		return url.URL{}, fmt.Errorf("%s not found", actualFunctionName)
	}
}
