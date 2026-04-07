package cli

import "github.com/spf13/cobra"

func RootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "docksmith",
		SilenceUsage: true,
		Short:        "Docksmith container image tooling",
	}

	cmd.CompletionOptions.DisableDefaultCmd = true

	cmd.AddCommand(
		buildCmd(),
		imagesCmd(),
		rmiCmd(),
		runCmd(),
	)

	return cmd
}
