# pogo-pvp-mcp

MCP server that will expose a Pokémon GO PvP battle simulator and ranker to
LLM assistants. The simulation math will live in a companion engine module
developed alongside this server.

**Status**: early development. The repository contains only scaffolding at
this point — no tools are implemented yet, and no tagged release exists. The
GitHub repository rename from `pvpoke-mcp` to `pogo-pvp-mcp` is pending, so
`go install github.com/lexfrei/pogo-pvp-mcp/...` does not yet resolve.

## Disclaimer

This project is not affiliated with, endorsed by, or sponsored by Niantic,
Inc., Nintendo, The Pokémon Company, Game Freak, or Creatures Inc. "Pokémon"
and related names are trademarks of their respective owners.

The server operates exclusively on factual game data (stat lines, movesets,
CPM values) fetched from the open-source [PvPoke][pvpoke] project (MIT
licensed). No artwork, sprites, or audio is distributed. Pokémon are
identified by string id only.

## Planned tools

None of the tools below are implemented yet. This section records the design
target so that tool names stay stable across planning documents.

- `pvp_rank` — rank one Pokémon in a league/cup by IV and level.
- `pvp_matchup` — 1v1 simulation with shield scenarios.
- `pvp_team_analysis` — evaluate a 3-member team against the meta.
- `pvp_team_builder` — Pareto-frontier team selection from a candidate pool.
- `pvp_meta` — current meta for a given format.

## License

BSD 3-Clause. See [LICENSE](LICENSE).

[pvpoke]: https://github.com/pvpoke/pvpoke
