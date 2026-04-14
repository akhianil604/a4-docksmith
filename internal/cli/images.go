package cli

import (
	"docksmith/internal/operations"

	"github.com/spf13/cobra"
)

func imagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "images",
		Short: "List images in the local store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return operations.Images(&operations.ImagesOpts{})
		},
	}

	return cmd
}
