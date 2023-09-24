package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	ioutil "io"
	"log"
	"net/http"

	"github.com/alanpjohn/uk-faas/pkg"
	function "github.com/alanpjohn/uk-faas/pkg/api/function"
	machine "github.com/alanpjohn/uk-faas/pkg/api/machine"
	"github.com/openfaas/faas-provider/types"
)

func MakeDeleteHandler(fStore function.FunctionService, mStore machine.MachineService) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Delete] request: %s\n", string(body))

		req := types.DeleteFunctionRequest{}
		err := json.Unmarshal(body, &req)
		if err != nil {
			log.Printf("[Delete] error parsing input: %s\n", err)
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
			log.Printf("[Delete] - error validating namespace: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if !valid {
			log.Printf("[Delete] - error validating namespace: %s\n", err)
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			return
		}

		name := req.FunctionName
		ctx := context.Background()
		if exists := fStore.FunctionExists(name); exists {
			log.Printf("[Delete] - Deleting instances: %s\n", name)
			err := mStore.StopAllMachines(ctx, name)
			if err != nil {
				msg := fmt.Sprintf("Function %s not deleted: %v", name, err)
				log.Printf("[Delete] %s\n", msg)
				http.Error(w, msg, http.StatusInternalServerError)
			}
			log.Printf("[Delete] - Deleting function: %s\n", name)
			err = fStore.DeleteFunction(ctx, name)
			if err != nil {
				msg := fmt.Sprintf("Function %s not deleted: %v", name, err)
				log.Printf("[Delete] %s\n", msg)
				http.Error(w, msg, http.StatusInternalServerError)
			}
			log.Printf("[Delete] - function deleted: %s\n", name)
		} else {
			msg := fmt.Sprintf("service %s not found", name)
			log.Printf("[Delete] %s\n", msg)
			http.Error(w, msg, http.StatusNotFound)
		}
	}
}
