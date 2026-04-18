# pogo-pvp-mcp

MCP server that will expose a Pokémon GO PvP battle simulator and ranker to
LLM assistants. The simulation math will live in a companion engine module
developed alongside this server.

**Status**: early development. Two tools are implemented:

- `pvp_rank` — rank one Pokémon in a league/cup by IV and level, with
  percent-of-best vs the species' global stat-product optimum.
- `pvp_matchup` — 1v1 simulation returning winner, turns, HP / energy /
  shields used, and charged-move firing counts.

`pvp_team_analysis`, `pvp_team_builder`, and `pvp_meta` are still
planned. No tagged release exists yet. The GitHub repository rename
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

Configuration goes through `$XDG_CONFIG_HOME/pogo-pvp-mcp/config.yaml`
(override via `--config`) or the `POGO_PVP_*` environment prefix. The
gamemaster is cached under `$XDG_CACHE_HOME/pogo-pvp-mcp/gamemaster.json`
by default.

## Disclaimer

This project is not affiliated with, endorsed by, or sponsored by Niantic,
Inc., Nintendo, The Pokémon Company, Game Freak, or Creatures Inc. "Pokémon"
and related names are trademarks of their respective owners.

The server operates exclusively on factual game data (stat lines, movesets,
CPM values) fetched from the open-source [PvPoke][pvpoke] project (MIT
licensed). No artwork, sprites, or audio is distributed. Pokémon are
identified by string id only.

## Planned tools

- `pvp_team_analysis` — evaluate a 3-member team against the meta.
- `pvp_team_builder` — Pareto-frontier team selection from a candidate pool.
- `pvp_meta` — current meta for a given format.

## License

BSD 3-Clause. See [LICENSE](LICENSE).

[pvpoke]: https://github.com/pvpoke/pvpoke
