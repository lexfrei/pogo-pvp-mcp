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
hard-coded defaults. The gamemaster is cached under
`$XDG_CACHE_HOME/pogo-pvp-mcp/gamemaster.json` by default.

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
