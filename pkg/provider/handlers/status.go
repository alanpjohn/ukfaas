package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/alanpjohn/uk-faas/pkg/store"
	"github.com/gorilla/mux"
)

func MakeFunctionStatusHandler(fStore *store.FunctionStore, mStore *store.MachineStore) func(w http.ResponseWriter, r *http.Request) {

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

		if found, err := fStore.GetFunctionStatus(service); err == nil {
			found.Replicas = mStore.GetReplicas(found.Name)
			found.AvailableReplicas = mStore.GetReplicas(found.Name)
			functionBytes, _ := json.Marshal(found)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(functionBytes)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}
}
