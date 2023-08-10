package handlers

import (
	"context"
	"testing"

	"github.com/openfaas/faas-provider/types"
)

func TestDeploy(t *testing.T) {
	ctx := context.Background()
	req := &types.FunctionDeployment{
		Image: "unikraft.org/uk-py-faas:latest",
	}
	err := Deploy(ctx, *req, "", false)
	if err != nil {
		t.Errorf("Deploy Failed: %+v", err)
	}
}
