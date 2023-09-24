package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	ioutil "io"
	"log"
	"net/http"
	"sync"

	functionapi "github.com/alanpjohn/uk-faas/pkg/api/function"
	machineapi "github.com/alanpjohn/uk-faas/pkg/api/machine"
	"github.com/openfaas/faas-provider/types"
)

func MakeUpdateHandler(fStore functionapi.FunctionService, mStore machineapi.MachineService) func(w http.ResponseWriter, r *http.Request) {

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Update] request: %s\n", string(body))

		req := types.FunctionDeployment{}
		err := json.Unmarshal(body, &req)
		if err != nil {
			log.Printf("[Update] error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		name := req.Service
		namespace := getRequestNamespace(req.Namespace)

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
		if function, err := fStore.GetFunctionStatus(ctx, name); err != nil {
			updatedFunction, updateImage, err := fStore.UpdateFunction(ctx, req)
			if err != nil {
				log.Printf("[Update] error updating %s, error: %s\n", name, err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if updateImage {
				var wg sync.WaitGroup
				errChan := make(chan error)

				wg.Add(1)
				wg.Add(1)

				go func(wg *sync.WaitGroup, errChan chan<- error) {
					defer wg.Done()
					err = mStore.StopAllMachines(ctx, name)
				}(&wg, errChan)
				go func(wg *sync.WaitGroup, errChan chan<- error) {
					defer wg.Done()
					err = mStore.NewMachine(ctx, updatedFunction)
				}(&wg, errChan)
				wg.Wait()
				close(errChan)
				if err != nil {
					log.Printf("[Update] error creating machine for %s, error: %s\n", name, err)
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
			err = mStore.ScaleMachinesTo(ctx, name, function.Replicas)
			if err != nil {
				log.Printf("[Update] error scaling machine for %s, error: %s\n", name, err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			msg := fmt.Sprintf("service %s not found", name)
			log.Printf("[Scale] %s\n", msg)
			http.Error(w, msg, http.StatusNotFound)
		}
	}
}
