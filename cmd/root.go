package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// GitCommit Git Commit SHA
	GitCommit string
	// Version version of the CLI
	Version string
)

func init() {
	rootCommand.AddCommand(versionCmd)
	rootCommand.AddCommand(installCmd)
	rootCommand.AddCommand(upCmd)
	rootCommand.AddCommand(makeProviderCmd())
	// rootCommand.AddCommand(collectCmd)
}

func RootCommand() *cobra.Command {
	return rootCommand
}

// Execute ukfaasd
func Execute(version, gitCommit string) error {

	// Get Version and GitCommit values from main.go.
	Version = version
	GitCommit = gitCommit

	if err := rootCommand.Execute(); err != nil {
		return err
	}
	return nil
}

var rootCommand = &cobra.Command{
	Use:          "uk-faas",
	Short:        "Start uk-faas",
	Long:         `uk-faas is a OpenFaaS provider that runs functions as unikernels`,
	RunE:         runRootCommand,
	SilenceUsage: true,
}

func runRootCommand(cmd *cobra.Command, args []string) error {
	cmd.Help()

	return nil
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Display version information.",
	Run:   parseBaseCommand,
}

func parseBaseCommand(_ *cobra.Command, _ []string) {
	printVersion()
}

func printVersion() {
	fmt.Printf("uk-faas version: %s\tcommit: %s\n", GetVersion(), GetGitCommit())
}

func GetVersion() string {
	if len(Version) == 0 {
		return "dirty"
	}
	return Version
}

// GetVersion get latest version
func GetGitCommit() string {
	if len(GitCommit) == 0 {
		return "dev"
	}
	return GitCommit
}
