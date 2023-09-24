package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	ukfaasd "github.com/alanpjohn/uk-faas/pkg"

	functionapi "github.com/alanpjohn/uk-faas/pkg/api/function"
)

func MakeNamespacesLister(fStore functionapi.FunctionService) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		list := ListNamespaces(fStore)
		body, _ := json.Marshal(list)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}

func ListNamespaces(fStore functionapi.FunctionService) []string {
	set := []string{}
	store := fStore.NamespaceService()
	namespaces, err := store.List(context.Background())
	if err != nil {
		log.Printf("Error listing namespaces: %s", err.Error())
		set = append(set, ukfaasd.DefaultFunctionNamespace)
		return set
	}

	for _, namespace := range namespaces {
		labels, err := store.Labels(context.Background(), namespace)
		if err != nil {
			log.Printf("Error listing label for namespace %s: %s", namespace, err.Error())
			continue
		}

		if _, found := labels[ukfaasd.NamespaceLabel]; found {
			set = append(set, namespace)
		}

		if !findNamespace(ukfaasd.DefaultFunctionNamespace, set) {
			set = append(set, ukfaasd.DefaultFunctionNamespace)
		}
	}

	return set
}

func findNamespace(target string, items []string) bool {
	for _, n := range items {
		if n == target {
			return true
		}
	}
	return false
}
