package cli

import (
	"fmt"
	"log/slog"

	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/spf13/cobra"
)

// newFetchGMCommand returns the "fetch-gm" subcommand: force a single
// gamemaster refresh from upstream and exit. Useful for populating the
// cache before an offline serve.
func newFetchGMCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "fetch-gm",
		Short: "Fetch the latest gamemaster from upstream and cache it locally",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt := runtimeFrom(cmd.Context())

			mgr, err := gamemaster.NewManager(rt.Config.Gamemaster)
			if err != nil {
				return fmt.Errorf("gamemaster manager: %w", err)
			}

			err = mgr.Refresh(cmd.Context())
			if err != nil {
				return fmt.Errorf("refresh: %w", err)
			}

			gm := mgr.Current()

			rt.Logger.Info("gamemaster cached",
				slog.Int("pokemon", len(gm.Pokemon)),
				slog.Int("moves", len(gm.Moves)),
				slog.String("version", gm.Version),
				slog.String("local_path", rt.Config.Gamemaster.LocalPath),
			)

			return nil
		},
	}
}
