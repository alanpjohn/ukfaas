package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"

	faasdlogs "github.com/alanpjohn/uk-faas/pkg/logs"
	"github.com/alanpjohn/uk-faas/pkg/network"
	"github.com/alanpjohn/uk-faas/pkg/provider/handlers"
	"github.com/alanpjohn/uk-faas/pkg/store"
	bootstrap "github.com/openfaas/faas-provider"
	"github.com/openfaas/faas-provider/logs"
	"github.com/openfaas/faas-provider/proxy"
	"github.com/openfaas/faas-provider/types"
	"github.com/openfaas/faasd/pkg/provider/config"
	"github.com/spf13/cobra"
)

func makeProviderCmd() *cobra.Command {
	var command = &cobra.Command{
		Use:   "provider",
		Short: "Run the ukfaasd",
	}

	command.Flags().String("pull-policy", "Always", `Set to "Always" to force a pull of images upon deployment, or "IfNotPresent" to try to use a cached image.`)

	command.RunE = func(_ *cobra.Command, _ []string) error {
		pullPolicy, flagErr := command.Flags().GetString("pull-policy")
		if flagErr != nil {
			return flagErr
		}

		alwaysPull := false
		if pullPolicy == "Always" {
			alwaysPull = true
		}

		config, providerConfig, err := config.ReadFromEnv(types.OsEnv{})
		if err != nil {
			return err
		}

		log.Printf("uk-faas provider starting..\tService Timeout: %s\n", config.WriteTimeout.String())

		wd, err := os.Getwd()
		if err != nil {
			return err
		}

		writeHostsErr := os.WriteFile(path.Join(wd, "hosts"),
			[]byte(`127.0.0.1	localhost\n127.0.0.1	ukfaas.dev`), workingDirectoryPermission)

		if writeHostsErr != nil {
			return fmt.Errorf("cannot write hosts file: %s", writeHostsErr)
		}

		writeResolvErr := os.WriteFile(path.Join(wd, "resolv.conf"),
			[]byte(`nameserver 8.8.8.8`), workingDirectoryPermission)

		if writeResolvErr != nil {
			return fmt.Errorf("cannot write resolv.conf file: %s", writeResolvErr)
		}

		ctx := context.Background()

		fStore, err := store.NewFunctionStore(ctx, providerConfig.Sock, "default")
		if err != nil {
			return err
		}

		defer fStore.Close()

		caddyController, err := network.NewCaddyController()
		if err != nil {
			return err
		}

		mStore, err := store.NewMachineStore(caddyController)
		if err != nil {
			return err
		}

		invokeResolver := handlers.NewInvokeResolver(fStore, mStore, caddyController)

		baseUserSecretsPath := path.Join(wd, "secrets")

		bootstrapHandlers := types.FaaSHandlers{
			FunctionProxy:  proxy.NewHandlerFunc(*config, invokeResolver),
			DeleteFunction: handlers.MakeDeleteHandler(fStore, mStore),
			DeployFunction: handlers.MakeDeployHandler(fStore, mStore, baseUserSecretsPath, alwaysPull),
			FunctionLister: handlers.MakeReadHandler(fStore),
			FunctionStatus: handlers.MakeFunctionStatusHandler(fStore, mStore),
			ScaleFunction:  handlers.MakeReplicaUpdateHandler(fStore, mStore),
			UpdateFunction: handlers.MakeUpdateHandler(fStore, mStore),
			Health:         func(w http.ResponseWriter, r *http.Request) {},
			Info:           handlers.MakeInfoHandler(Version, GitCommit),
			ListNamespaces: handlers.MakeNamespacesLister(fStore),
			// Secrets:         handlers.MakeSecretHandler(client.NamespaceService(), baseUserSecretsPath),
			Logs: logs.NewLogHandlerFunc(faasdlogs.New(), config.ReadTimeout),
			// MutateNamespace: handlers.MakeMutateNamespace(client),
		}

		log.Printf("Listening on: 0.0.0.0:%d\n", *config.TCPPort)
		bootstrap.Serve(&bootstrapHandlers, config)
		return nil
	}
	return command
}
