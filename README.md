# pogo-pvp-mcp

MCP server that will expose a Pokémon GO PvP battle simulator and ranker to
LLM assistants. The simulation math will live in a companion engine module
developed alongside this server.

**Status**: early development. Five tools are implemented:

- `pvp_rank` — rank one Pokémon in a league/cup by IV and level, with
  percent-of-best vs the species' global stat-product optimum.
- `pvp_matchup` — 1v1 simulation returning winner, turns, HP / energy /
  shields used, and charged-move firing counts.
- `pvp_meta` — top-N species from pvpoke's overall rankings for a
  league, including recommended moveset and display stats.
- `pvp_team_analysis` — evaluate a 3-member team against the sampled
  meta: per-member battle ratings, hard wins / losses, coverage
  matrix, and uncovered threats.
- `pvp_team_builder` — enumerate 3-member teams from a candidate pool,
  score each against the meta, return the highest-scoring subset
  (required / banned species filters honoured).

No tagged release exists yet. The GitHub repository rename
from `pvpoke-mcp` to `pogo-pvp-mcp` is pending, so
`go install github.com/lexfrei/pogo-pvp-mcp/cmd/pogo-pvp-mcp@latest`
does not yet resolve.

## Running locally

The companion engine module lives in a sibling directory during early
development and is wired through a `replace` directive in `go.mod`:

```text
replace github.com/lexfrei/pogo-pvp-engine => ../pogo-pvp-engine
```

Clone both repos side-by-side under the same parent, then:

```bash
go build ./cmd/pogo-pvp-mcp
./pogo-pvp-mcp fetch-gm    # warm the local cache from upstream
./pogo-pvp-mcp serve       # run over MCP stdio
```

Configuration flows through `--config path/to/config.yaml` (optional)
and the `POGO_PVP_*` environment prefix. `POGO_PVP_CONFIG` is honoured
as the default for `--config`, so you can set it once in your shell
instead of repeating the flag. There is no XDG or standard-path config
lookup — either `--config`, `POGO_PVP_CONFIG`, or env overrides +
hard-coded defaults.

Two filesystem caches live alongside each other by default:

- `$XDG_CACHE_HOME/pogo-pvp-mcp/gamemaster.json` — the upstream
  pvpoke gamemaster, refreshed every 24h or forced via `fetch-gm`.
- `$XDG_CACHE_HOME/pogo-pvp-mcp/rankings/rankings-{500,1500,2500,10000}.json` —
  per-league pvpoke rankings, fetched lazily the first time a meta-
  driven tool (`pvp_meta`, `pvp_team_analysis`, `pvp_team_builder`)
  touches that cap. Each file expires after 24h and is re-fetched on
  the next access.

## Debug HTTP surface

Setting `server.http_port` (or `POGO_PVP_SERVER_HTTP_PORT`) to a
non-zero port opens a small debug surface on top of the MCP stdio
transport:

- `GET  /healthz` — 200 when the gamemaster is loaded, 503 otherwise.
- `POST /refresh` — synchronous upstream gamemaster refresh.
- `GET  /debug/gamemaster` — Pokémon / move counts + version string.

It binds `127.0.0.1` by default; override via `server.http_host` if
you need to expose it externally (don't — it's intended for local
readiness probes and on-demand cache primes).

## Claude Desktop integration

Add the server to `~/Library/Application Support/Claude/claude_desktop_config.json`
(macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "pogo-pvp": {
      "command": "/absolute/path/to/pogo-pvp-mcp",
      "args": ["serve"]
    }
  }
}
```

Restart Claude Desktop. The five `pvp_*` tools will appear in the
tool list. If a tool returns "gamemaster not loaded", run
`pogo-pvp-mcp fetch-gm` once to warm the cache.

## Container image

A `Containerfile` ships in the repo root; tagged builds produce
multi-arch (linux/amd64 + linux/arm64, cosign-signed) images at
`ghcr.io/${GITHUB_REPOSITORY}:vX.Y.Z`. Until the GitHub repo is
renamed from `pvpoke-mcp` to `pogo-pvp-mcp`, the effective image
coordinate is `ghcr.io/lexfrei/pvpoke-mcp:vX.Y.Z`; after the rename
it flips to `ghcr.io/lexfrei/pogo-pvp-mcp:vX.Y.Z` without any
workflow change (the release workflow reads `${{ github.repository }}`).

Note: the image build depends on
`github.com/lexfrei/pogo-pvp-engine` being resolvable by `go mod
download` — during the engine-sibling development window (while the
`replace` directive in `go.mod` points at a local `../pogo-pvp-engine`
checkout), the Containerfile will not build cleanly. It becomes
buildable once the engine repository is published and tagged.

## Disclaimer

This project is not affiliated with, endorsed by, or sponsored by Niantic,
Inc., Nintendo, The Pokémon Company, Game Freak, or Creatures Inc. "Pokémon"
and related names are trademarks of their respective owners.

The server operates exclusively on factual game data (stat lines, movesets,
CPM values) fetched from the open-source [PvPoke][pvpoke] project (MIT
licensed). No artwork, sprites, or audio is distributed. Pokémon are
identified by string id only.

## Roadmap

- Full battle-simulation-based ranker (engine-side) so `pvp_meta`
  stops depending on pre-computed pvpoke JSONs.
- CMP / shadow scaling in the battle engine.
- Parallel `pvp_team_builder` worker pool for large pools.

## License

BSD 3-Clause. See [LICENSE](LICENSE).

[pvpoke]: https://github.com/pvpoke/pvpoke
