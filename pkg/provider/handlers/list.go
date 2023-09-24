package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	functionapi "github.com/alanpjohn/uk-faas/pkg/api/function"
	machineapi "github.com/alanpjohn/uk-faas/pkg/api/machine"
)

func MakeReadHandler(fStore functionapi.FunctionService, mStore machineapi.MachineService) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		lookupNamespace := getRequestNamespace(readNamespaceFromQuery(r))

		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(fStore.NamespaceService(), lookupNamespace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			log.Printf("[List] - error validating namespace: %s\n", err)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			log.Printf("[List] - error validating namespace: %s\n", err)
			return
		}

		ctx := context.Background()
		res, err := fStore.ListFunctions(ctx)
		for index, function := range res {
			function.Replicas = mStore.GetReplicas(ctx, function.Name)
			function.AvailableReplicas = mStore.GetAvailableReplicas(ctx, function.Name)
			res[index] = function
		}

		if err != nil {
			log.Printf("[List] error listing functions. Error: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		body, _ := json.Marshal(res)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}
