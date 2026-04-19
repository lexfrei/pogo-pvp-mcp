package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	pogopvp "github.com/lexfrei/pogo-pvp-engine"
	"github.com/lexfrei/pogo-pvp-mcp/internal/gamemaster"
	"github.com/spf13/cobra"
)

// diffGMFetchTimeout caps the one-shot upstream GET that diff-gm
// issues. Matches the 30s timeout used by rankings.Manager so a slow
// pvpoke CDN does not hang a cron-driven drift check indefinitely —
// `http.DefaultClient` has no timeout at all. Declared as a var so
// tests can drop it below 30s without waiting out the real ceiling;
// production code never reassigns it.
//
//nolint:gochecknoglobals // test-overridable timeout, never reassigned in production
var diffGMFetchTimeout = 30 * time.Second

// ErrDiffDirty signals a non-empty diff so cron / CI drivers can
// exit 1 without further plumbing. The command prints the full
// diff to stdout first, so the error message itself stays terse.
var ErrDiffDirty = errors.New("gamemaster: differences detected")

// ErrUpstreamStatus wraps non-200 HTTP responses from the one-shot
// upstream fetch used by diff-gm. Separate from the similarly-named
// rankings.ErrUpstreamStatus because the two packages must not
// depend on each other.
var ErrUpstreamStatus = errors.New("gamemaster upstream returned non-200")

// diffGMFlags captures the --against override. Omitted → compare
// upstream (config.gamemaster.source) with the local cache
// (config.gamemaster.local_path). Both need to exist for the
// command to produce a non-trivial diff.
type diffGMFlags struct {
	against string
}

// newDiffGMCommand returns the "diff-gm" subcommand: download the
// current upstream gamemaster (or read a file via --against) and
// diff it against the local cache, printing a human-readable report.
// Exits 0 when there are no changes, 1 on a non-empty diff — handy
// for cron / CI alerts.
func newDiffGMCommand() *cobra.Command {
	flags := &diffGMFlags{}

	cmd := &cobra.Command{
		Use:   "diff-gm",
		Short: "Diff the upstream gamemaster against the locally cached copy",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDiffGM(cmd.Context(), flags)
		},
	}

	cmd.Flags().StringVar(&flags.against, "against", "",
		"path to a gamemaster JSON file to compare against the local cache (default: fetch upstream)")

	return cmd
}

// runDiffGM orchestrates the two-sided load and prints the diff.
// Split from newDiffGMCommand so tests can drive it directly.
func runDiffGM(ctx context.Context, flags *diffGMFlags) error {
	rt := runtimeFrom(ctx)

	local, err := loadLocalGamemaster(rt.Config.Gamemaster.LocalPath)
	if err != nil {
		return fmt.Errorf("load local gamemaster: %w", err)
	}

	remote, err := loadRemoteGamemaster(ctx, rt.Config.Gamemaster.Source, flags.against)
	if err != nil {
		return fmt.Errorf("load remote gamemaster: %w", err)
	}

	diff := gamemaster.DiffGamemasters(local, remote)
	gamemaster.WriteDiff(rt.Stdout, &diff)

	if !diff.Empty() {
		return ErrDiffDirty
	}

	return nil
}

// loadLocalGamemaster reads the cache file and parses it. A missing
// file is treated as "empty baseline" — useful for the first-ever
// diff against a fresh clone — so the full remote content shows up
// as adds.
func loadLocalGamemaster(path string) (*pogopvp.Gamemaster, error) {
	if path == "" {
		return nil, nil //nolint:nilnil // empty cache path means "no baseline yet"
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil //nolint:nilnil // first-run: no cache yet
		}

		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	gm, err := pogopvp.ParseGamemaster(file)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return gm, nil
}

// loadRemoteGamemaster chooses the HTTP source or an on-disk
// override based on the --against flag. The override is strictly
// local — we never interpret it as a URL — so operators can diff
// two saved snapshots without touching the network.
func loadRemoteGamemaster(
	ctx context.Context, source, against string,
) (*pogopvp.Gamemaster, error) {
	if against != "" {
		return loadLocalGamemaster(against)
	}

	return fetchUpstreamGamemaster(ctx, source)
}

// fetchUpstreamGamemaster does a one-shot GET of the configured
// source URL without going through gamemaster.Manager — we do not
// want to mutate the cache as a side effect of a diff command. Uses
// a dedicated client with diffGMFetchTimeout so a slow / hung
// upstream cannot hang a cron-driven drift check indefinitely.
func fetchUpstreamGamemaster(
	ctx context.Context, source string,
) (*pogopvp.Gamemaster, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	client := &http.Client{Timeout: diffGMFetchTimeout}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", source, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status=%d url=%s",
			ErrUpstreamStatus, resp.StatusCode, source)
	}

	return parseFromReader(resp.Body)
}

// parseFromReader is a tiny wrapper so the HTTP and file paths
// share the same decode call.
func parseFromReader(reader io.Reader) (*pogopvp.Gamemaster, error) {
	gm, err := pogopvp.ParseGamemaster(reader)
	if err != nil {
		return nil, fmt.Errorf("parse gamemaster: %w", err)
	}

	return gm, nil
}
