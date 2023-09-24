package machine

import (
	"context"

	"github.com/alanpjohn/uk-faas/pkg/api/function"
	"github.com/alanpjohn/uk-faas/pkg/api/network"
	"kraftkit.sh/api/machine/v1alpha1"
)

type MachineID string

type Machine v1alpha1.Machine

type MachineServiceType string

type MachineService interface {
	GetMachines(context.Context, string) ([]Machine, error)
	StopAllMachines(context.Context, string) error
	RunHealthChecks(context.Context) error

	GetReplicas(context.Context, string) uint64
	GetAvailableReplicas(context.Context, string) uint64

	ScaleMachinesTo(context.Context, string, uint64) error
	NewMachine(context.Context, function.Function) error

	NetworkService() network.NetworkService
}
