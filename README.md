# pogo-pvp-mcp

MCP server exposing a Pokémon GO PvP battle simulator and ranker to LLM
assistants. Backed by [`pogo-pvp-engine`][engine] — a pure Go reimplementation
of the PvP math.

**Status**: early development. No stable release yet.

## Disclaimer

This project is not affiliated with, endorsed by, or sponsored by Niantic,
Inc., Nintendo, The Pokémon Company, Game Freak, or Creatures Inc. "Pokémon"
and related names are trademarks of their respective owners.

The server operates exclusively on factual game data (stat lines, movesets,
CPM values) fetched from the open-source [PvPoke][pvpoke] project (MIT
licensed). No artwork, sprites, or audio is distributed. Pokémon are
identified by string id only.

## Tools

- `pvp_rank` — rank one Pokémon in a league/cup by IV and level.
- `pvp_matchup` — 1v1 simulation with shield scenarios.
- `pvp_team_analysis` — evaluate a 3-member team against the meta.
- `pvp_team_builder` — Pareto-frontier team selection from a candidate pool.
- `pvp_meta` — current meta for a given format.

## License

BSD 3-Clause. See [LICENSE](LICENSE).

[engine]: https://github.com/lexfrei/pogo-pvp-engine
[pvpoke]: https://github.com/pvpoke/pvpoke
