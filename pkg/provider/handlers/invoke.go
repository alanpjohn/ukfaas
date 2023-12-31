package handlers

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/alanpjohn/uk-faas/pkg"
	networkapi "github.com/alanpjohn/uk-faas/pkg/api/network"
	"github.com/alanpjohn/uk-faas/pkg/store"
)

type InvokeResolver struct {
	fStore       *store.FunctionStore
	mStore       *store.MachineStore
	networkStore networkapi.NetworkController
}

func NewInvokeResolver(f *store.FunctionStore, m *store.MachineStore, n networkapi.NetworkController) *InvokeResolver {
	return &InvokeResolver{
		fStore:       f,
		mStore:       m,
		networkStore: n,
	}
}

func (i *InvokeResolver) Resolve(functionName string) (url.URL, error) {
	actualFunctionName := functionName
	if strings.Contains(functionName, ".") {
		actualFunctionName = strings.TrimSuffix(functionName, "."+pkg.DefaultFunctionNamespace)
	}
	if function, err := i.fStore.GetFunction(actualFunctionName); err == nil {
		if i.mStore.GetReplicas(actualFunctionName) == 0 {
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
