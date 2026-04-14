package cli

import (
	"docksmith/internal/operations"

	"github.com/spf13/cobra"
)

func rmiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rmi <name:tag>",
		Short: "Remove an image manifest and all of its layer files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return operations.RMI(&operations.RMIOpts{Reference: args[0]})
		},
	}

	return cmd
}
