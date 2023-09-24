package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	ioutil "io"
	"log"
	"net/http"
	"os"
	"path"

	functionapi "github.com/alanpjohn/uk-faas/pkg/api/function"
	machineapi "github.com/alanpjohn/uk-faas/pkg/api/machine"
	"github.com/containerd/containerd/namespaces"
	"github.com/openfaas/faas-provider/types"
)

func MakeDeployHandler(fStore functionapi.FunctionService, mStore machineapi.MachineService, secretMountPath string, alwaysPull bool) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil {
			http.Error(w, "expected a body", http.StatusBadRequest)
			return
		}

		defer r.Body.Close()

		body, _ := ioutil.ReadAll(r.Body)
		log.Printf("[Deploy] request: %s\n", string(body))

		req := types.FunctionDeployment{}
		err := json.Unmarshal(body, &req)
		if err != nil {
			log.Printf("[Deploy] - error parsing input: %s\n", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		namespace := getRequestNamespace(req.Namespace)

		// Check if namespace exists, and it has the openfaas label
		valid, err := validNamespace(fStore.NamespaceService(), namespace)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			log.Printf("[Deploy] - error validating namespace: %s\n", err)
			return
		}

		if !valid {
			http.Error(w, "namespace not valid", http.StatusBadRequest)
			log.Printf("[Deploy] - error validating namespace: %s\n", err)
			return
		}

		namespaceSecretMountPath := getNamespaceSecretMountPath(secretMountPath, namespace)
		err = validateSecrets(namespaceSecretMountPath, req.Secrets)

		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			log.Printf("[Deploy] - error validating secrets: %s\n", err)
			return
		}

		name := req.Service
		ctx := namespaces.WithNamespace(context.Background(), namespace)

		log.Printf("[Deploy] request: Creating service - %s\n", name)
		function, err := fStore.AddFunction(ctx, req)
		if err != nil {
			log.Printf("[Deploy] error pulling %s, error: %s\n", name, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		log.Printf("[Deploy] request: Creating machine - %s\n", function.Image)
		err = mStore.NewMachine(ctx, function)
		if err != nil {
			log.Printf("[Deploy] error running machine %s, error: %s\n", name, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		log.Printf("[Deploy] request: Deployed service - %s\n", function.Service)
		w.WriteHeader(http.StatusOK)

	}
}

func validateSecrets(secretMountPath string, secrets []string) error {
	for _, secret := range secrets {
		if _, err := os.Stat(path.Join(secretMountPath, secret)); err != nil {
			return fmt.Errorf("unable to find secret: %s", secret)
		}
	}
	return nil
}
