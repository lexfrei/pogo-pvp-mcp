// Command pogo-pvp-mcp runs the MCP server exposing Pokémon GO PvP
// tooling. It's a thin wrapper: all orchestration lives in
// internal/cli so the cobra tree can be exercised from tests without
// booting a real process.
package main

import (
	"os"

	"github.com/lexfrei/pogo-pvp-mcp/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
