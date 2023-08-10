package store

import (
	"time"

	"github.com/openfaas/faas-provider/types"
	"kraftkit.sh/pack"
)

type MachineID string
type FunctionID string

type FunctionMetaData struct {
	CreatedAt time.Time
	types.FunctionDeployment
	pack.Package
	StorageDir string
}
