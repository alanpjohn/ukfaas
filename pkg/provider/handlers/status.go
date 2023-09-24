package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	functionapi "github.com/alanpjohn/uk-faas/pkg/api/function"
	machineapi "github.com/alanpjohn/uk-faas/pkg/api/machine"
	"github.com/gorilla/mux"
)

func MakeFunctionStatusHandler(fStore functionapi.FunctionService, mStore machineapi.MachineService) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		service := vars["name"]
		lookupNamespace := getRequestNamespace(readNamespaceFromQuery(r))

		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(fStore.NamespaceService(), lookupNamespace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		if found, err := fStore.GetFunctionStatus(ctx, service); err == nil {
			found.Replicas = mStore.GetReplicas(ctx, service)
			found.AvailableReplicas = mStore.GetAvailableReplicas(ctx, service)
			functionBytes, _ := json.Marshal(found)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(functionBytes)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}
}
