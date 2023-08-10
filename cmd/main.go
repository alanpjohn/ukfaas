package main

import (
	"fmt"
	"os"
)

func main() {

	if _, ok := os.LookupEnv("CONTAINER_ID"); ok {
		collect := RootCommand()
		collect.SetArgs([]string{"collect"})
		collect.SilenceUsage = true
		collect.SilenceErrors = true

		err := collect.Execute()
		if err != nil {
			fmt.Fprint(os.Stderr, err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := Execute(Version, GitCommit); err != nil {
		os.Exit(1)
	}
}
