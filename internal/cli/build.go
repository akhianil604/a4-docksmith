package cli

import (
	"docksmith/internal/operations"

	"github.com/spf13/cobra"
)

func buildCmd() *cobra.Command {
	var tag string
	var noCache bool

	cmd := &cobra.Command{
		Use:   "build -t <name:tag> <context>",
		Short: "Build an image from a Docksmithfile in a context directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			contextDir := args[0]
			return operations.Build(&operations.BuildOpts{
				Tag:     tag,
				Context: contextDir,
				NoCache: noCache,
			})
		},
	}

	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Image name and tag in the form name:tag")
	_ = cmd.MarkFlagRequired("tag")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "Skip all cache lookups and writes for this build")

	return cmd
}
