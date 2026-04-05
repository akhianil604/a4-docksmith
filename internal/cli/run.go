package cli

import (
	"fmt"
	"strings"

	"docksmith/internal/operations"

	"github.com/spf13/cobra"
)

func runCmd() *cobra.Command {
	var envPairs []string

	cmd := &cobra.Command{
		Use:   "run <name:tag> [cmd]",
		Short: "Assemble the filesystem and run the container in the foreground",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := parseEnvPairs(envPairs)
			if err != nil {
				return err
			}

			opts := &operations.RunOpts{
				Reference: args[0],
				Env:       env,
			}
			if len(args) > 1 {
				opts.Cmd = args[1:]
			}

			return operations.Run(opts)
		},
	}

	cmd.Flags().StringArrayVarP(&envPairs, "env", "e", nil, "Override or add environment variables (KEY=VALUE); repeatable")

	return cmd
}

func parseEnvPairs(pairs []string) (map[string]string, error) {
	env := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		if !strings.Contains(pair, "=") {
			return nil, fmt.Errorf("invalid -e value %q: expected KEY=VALUE", pair)
		}

		k, v, _ := strings.Cut(pair, "=")
		if strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("invalid -e value %q: key cannot be empty", pair)
		}

		env[k] = v
	}

	return env, nil
}
