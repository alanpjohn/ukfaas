package v1

import (
	"github.com/containerd/containerd"
)

type FunctionServiceV1Option func(*FunctionStoreV1) error

func WithContainerHandler(containerdAddr string, defaultNamespace string) FunctionServiceV1Option {
	return func(fsv *FunctionStoreV1) error {
		client, err := containerd.New(containerdAddr, containerd.WithDefaultNamespace(defaultNamespace))
		if err != nil {
			return err
		}
		fsv.client = client
		return nil
	}
}
