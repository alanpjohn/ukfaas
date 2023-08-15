package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	network "github.com/alanpjohn/uk-faas/pkg/network"
	"github.com/alanpjohn/uk-faas/pkg/store"
	"github.com/openfaas/faas-provider/types"
	"github.com/spf13/cobra"
)

func init() {
	rootCommand.AddCommand(testCmd)
}

var testCmd = &cobra.Command{
	Use:   "test",
	Short: "testri faasd",
	RunE:  runTest,
}

func runTest(_ *cobra.Command, args []string) error {
	ctx := context.Background()
	fStore, err := store.NewFunctionStore(ctx, "/run/containerd/containerd.sock", "default")
	if err != nil {
		return err
	}

	defer fStore.Close()

	networkController, err := network.GetNetworkController("internal")
	if err != nil {
		return err
	}

	go networkController.RunHealthChecks(ctx)

	mStore, err := store.NewMachineStore(networkController)
	if err != nil {
		return err
	}

	req := types.FunctionDeployment{
		Image:       args[0],
		Service:     "test-func",
		EnvVars:     map[string]string{},
		Secrets:     []string{},
		Labels:      &map[string]string{},
		Annotations: &map[string]string{},
		Namespace:   "openfaas-fn",
	}
	// Create function

	function, err := fStore.AddFunction(ctx, req)
	if err != nil {
		return err
	}

	err = mStore.NewMachine(ctx, function)
	if err != nil {
		return err
	}

	fmt.Println("Press ENTER to scale up...")
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')

	err = mStore.ScaleMachinesTo(ctx, req.Service, 2)
	if err != nil {
		return err
	}

	sigChan := make(chan os.Signal, 1)

	// Register the channel to receive SIGINT (Ctrl+C) and SIGTERM signals
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Block until a signal is received
	fmt.Println("Waiting for Ctrl+C (SIGINT) or kill signal (SIGTERM)...")
	<-sigChan

	fmt.Println("Signal received. Exiting...")

	err = mStore.StopAllMachines(ctx, req.Service)

	return err
}
