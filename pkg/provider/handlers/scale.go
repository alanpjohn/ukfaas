package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	ioutil "io"
	"log"
	"net/http"

	"github.com/alanpjohn/uk-faas/pkg"
	functionapi "github.com/alanpjohn/uk-faas/pkg/api/function"
	machineapi "github.com/alanpjohn/uk-faas/pkg/api/machine"
	"github.com/openfaas/faas-provider/types"
)

func MakeReplicaUpdateHandler(fStore functionapi.FunctionService, mStore machineapi.MachineService) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Scale] request: %s\n", string(body))

		req := types.ScaleServiceRequest{}
		if err := json.Unmarshal(body, &req); err != nil {
			log.Printf("[Scale] error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		namespace := req.Namespace
		if namespace == "" {
			namespace = pkg.DefaultFunctionNamespace
		}

		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(fStore.NamespaceService(), namespace)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		if function, err := fStore.GetFunction(ctx, req.ServiceName); err == nil {
			if mStore.GetReplicas(ctx, req.ServiceName) == 0 {
				mStore.NewMachine(ctx, function)
			}
			err := mStore.ScaleMachinesTo(ctx, req.ServiceName, req.Replicas)
			if err != nil {
				msg := fmt.Sprintf("Function %s not scaled: %v", req.ServiceName, err)
				log.Printf("[Scale] %s\n", msg)
				http.Error(w, msg, http.StatusInternalServerError)
			}
		} else {
			msg := fmt.Sprintf("service %s not found : %v", req.ServiceName, err)
			log.Printf("[Scale] %s\n", msg)
			http.Error(w, msg, http.StatusNotFound)
		}
	}
}
