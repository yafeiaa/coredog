package main

import (
	"github.com/DomineCore/coredog/internal/agent"
	"github.com/spf13/cobra"
)

func main() {
	root := cobra.Command{}

	watcherBootstrap := cobra.Command{
		Use: "watcher",
		RunE: func(cmd *cobra.Command, args []string) error {
			agent.Run()
			return nil
		},
		Long: "start a watcher agent on host to watch corefile created.",
	}

	root.AddCommand(&watcherBootstrap)
	root.Execute()
}
