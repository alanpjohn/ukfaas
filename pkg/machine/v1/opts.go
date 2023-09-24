package v1

import (
	networkapi "github.com/alanpjohn/uk-faas/pkg/api/network"
)

type MachineServiceV1Option func(*MachineServiceV1) error

func WithNetworkService(nst networkapi.NetworkServiceType) MachineServiceV1Option {
	return func(msv *MachineServiceV1) error {
		return nil
	}
}
