package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/alanpjohn/uk-faas/pkg/store"
)

func MakeReadHandler(fStore *store.FunctionStore, mStore *store.MachineStore) func(w http.ResponseWriter, r *http.Request) {

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

		// fns, err := ListFunctions(client, lookupNamespace)
		res, err := fStore.ListFunctions()
		for _, function := range res {
			function.Replicas = mStore.GetReplicas(function.Name)
			function.AvailableReplicas = mStore.GetAvailableReplicas(function.Name)
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
