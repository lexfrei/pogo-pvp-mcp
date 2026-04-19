// Package cli wires the server's cobra command tree and shares the
// loaded Config across subcommands via a runtime struct pointer.
package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/lexfrei/pogo-pvp-mcp/internal/config"
	"github.com/lexfrei/pogo-pvp-mcp/internal/logging"
	"github.com/spf13/cobra"
)

// Runtime is the shared state the subcommands build on top of: the
// loaded Config, a ready slog.Logger, and the writers to use for stdout
// / stderr. Tests inject alternate writers; main wires os.Stdout /
// os.Stderr.
type Runtime struct {
	Config *config.Config
	Logger *slog.Logger
	Stdout io.Writer
	Stderr io.Writer
}

// rootFlags stores the CLI flags registered on the root command.
type rootFlags struct {
	configPath string
}

// NewRootCommand builds the cobra command tree: the root command plus
// the serve and fetch-gm subcommands. Running the bare binary prints
// usage; there is no implicit default subcommand.
func NewRootCommand(stdout, stderr io.Writer) *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:           "pogo-pvp-mcp",
		Short:         "Pokémon GO PvP MCP server",
		Long:          "MCP server exposing a Pokémon GO PvP battle simulator and ranker.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// POGO_PVP_CONFIG env overrides the default empty config path when
	// the flag is not passed. This matches the user-facing promise in
	// the README that every setting has a POGO_PVP_* equivalent.
	defaultConfigPath := os.Getenv("POGO_PVP_CONFIG")

	root.PersistentFlags().StringVar(&flags.configPath, "config", defaultConfigPath,
		"path to config.yaml; overrides POGO_PVP_CONFIG env var")

	runtimeBuilder := func(cmd *cobra.Command, _ []string) error {
		rt, err := buildRuntime(flags.configPath, stdout, stderr)
		if err != nil {
			return err
		}

		cmd.SetContext(withRuntime(cmd.Context(), rt))

		return nil
	}

	root.PersistentPreRunE = runtimeBuilder

	root.AddCommand(newServeCommand())
	root.AddCommand(newFetchGMCommand())
	root.AddCommand(newDiffGMCommand())

	return root
}

// buildRuntime loads + validates the config, wires up the logger, and
// returns the aggregate struct the subcommands read from.
func buildRuntime(configPath string, stdout, stderr io.Writer) (*Runtime, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	logger, err := logging.NewLogger(cfg.Log, stderr)
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}

	return &Runtime{
		Config: cfg,
		Logger: logger,
		Stdout: stdout,
		Stderr: stderr,
	}, nil
}

// Execute is the entry point called from main. It wires os.Stdin /
// os.Stdout / os.Stderr, runs the command tree, and translates any
// error into a non-zero exit code.
func Execute() int {
	root := NewRootCommand(os.Stdout, os.Stderr)

	err := root.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pogo-pvp-mcp: %v\n", err)

		return 1
	}

	return 0
}
