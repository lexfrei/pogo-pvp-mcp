# pogo-pvp-mcp

MCP server that will expose a Pokémon GO PvP battle simulator and ranker to LLM assistants. The simulation math will live in a companion engine module developed alongside this server.

**Status**: approaching v0.1. Twenty-two MCP tools plus a `diff-gm` CLI helper are implemented:

- `pvp_rank` — rank one Pokémon in a league by IV and level, with percent-of-best vs the species' global stat-product optimum, a pvpoke-recommended `optimal_moveset` carrying an aggregate `has_legacy` boolean, a `non_legacy_moveset` alternative (populated only when the optimal build contains at least one legacy move, with `rating_delta` vs the optimal build), a `comparison_to_hundo` block showing the best-case 15/15/15 spread, and a `rankings_by_cup` array carrying the species' position in every pvpoke-published cup ranking for the league (open-league first, then named cups — `spring` / `retro` / `jungle` / etc. — sorted by id; cups where the species is not in pvpoke's per-cup list are omitted). The top-level moveset / non-legacy / hundo are computed from the open-league rankings; per-cup moveset is included in each `rankings_by_cup` entry when pvpoke publishes one. **Breaking change: the `cup` input parameter is removed** — one call now returns all cups for the league, so the old per-cup drift (`cup=spring` returning open-league `percent_of_best`) can't happen. `cp_cap` is an optional override of the league default (500 / 1500 / 2500 / 10000 for little / great / ultra / master); passing a positive value re-searches the optimal level under that cap, so `Level` / `CP` / `StatProduct` / `PercentOfBest` all reflect the overridden cap, not the league default. Use-case: exploring hypothetical tournament formats (e.g. `cp_cap=2000` with `league=ultra`). The `rankings_by_cup` lookup also uses the resolved cap — so overriding with a value that does not match a pvpoke-published cap (anything other than 500 / 1500 / 2500 / 10000) returns an empty `rankings_by_cup` array because pvpoke publishes per-cup rankings only at standard league caps.
- `pvp_matchup` — 1v1 simulation returning winner, turns, HP / energy / shields used, charged-move firing counts, and the resolved moveset used on each side (so omitted `fast_move` / `charged_moves` get auto-filled from the cup/league recommended build).
- `pvp_cp_limits` — given species + IVs, return the highest level and CP reachable while staying under each PvP league's CP cap.
- `pvp_meta` — top-N species from pvpoke's per-(cup, league) rankings. Each entry carries the recommended moveset as a `[]MoveRef{id, legacy}` slice so per-move legacy status is explicit, plus display stats and a role classification (`lead` / `switch` / `closer` / `flex`) from the per-role pvpoke rankings.
- `pvp_team_analysis` — evaluate a 3-member team against the sampled meta. Response splits into `overall` (mean-of-means across every requested shield scenario) and `per_scenario` (keyed as `"0s"`, `"1s"`, `"2s"`); each block carries the same shape — `team_score`, `per_member` (with hard wins / losses, resolved moveset, and a `cost_breakdown` for powerup + second-move stardust / candy), `coverage_matrix`, `uncovered_threats`. A `disallow_legacy` flag (default false) rejects explicit legacy moves with `ErrLegacyConflict` before simulation and prevents the auto-fill path from landing on a legacy recommendation. `target_level` drives the powerup cost estimation same as `pvp_team_builder` (omit or 0 → per-species deepest level fitting the league cap at 15/15/15 IVs; positive value → exact 0.5-grid level; if a member is already at or above the resolved target the powerup cost clamps to zero with `already_at_or_above_target=true`); Options multipliers (shadow / purified / lucky) apply. Team members whose level-1 CP already exceeds the league cap fail fast with `ErrMemberInvalidForLeague`.
- `pvp_team_builder` — enumerate 3-member teams from a candidate pool and score each against a per-scenario rating matrix. The `optimize_for` parameter selects the scoring axis (`overall` / `0s` / `1s` / `2s` / `all_pareto`); `all_pareto` returns up to four teams (best overall plus best-in-class per shield scenario). Required / banned species filters honoured. `disallow_legacy` (default false) rejects explicit legacy moves with `ErrLegacyConflict` and forces the auto-fill path to skip legacy-containing pvpoke recommendations. `auto_evolve` (default false) walks each pool member up its evolution chain to the deepest descendant that still fits the league cap at level 1. Linear chains promote silently and emit an `auto_evolved_from:<orig>` breakdown flag (full-terminal promotion AND partial walks where the chain's terminal busts the cap both end up here — the flag only records that evolution happened, not how far). Branching chains (eevee → vaporeon/jolteon/flareon) leave the base form with `auto_evolve_skipped_branching:<orig>`. First-hop over-cap (base's immediate next evolution already busts the cap at L1) leaves the base form with `auto_evolve_over_cap:<orig>`. Filter semantics with auto-evolve: `required` matches the POST-evolve species id, so `"required": ["squirtle"]` + `"auto_evolve": true` produces `ErrRequiredNotInPool` once squirtle becomes blastoise (the caller should list the post-evolve id in `required`). `banned` matches EITHER the original or the evolved species id, so `"banned": ["squirtle"]` + `"auto_evolve": true` still filters the pool entry even after it's promoted to blastoise — banning honours caller intent whichever form of the id they knew about. `options.shadow=true` survives the promotion: shadow squirtle becomes shadow blastoise (with `shadow_variant_missing=true` if pvpoke doesn't publish a shadow row for the evolved species). Explicit `fast_move` / `charged_moves` on a promoted pool entry are cleared — the base species' recommended moveset is not valid on the descendant, so the rankings re-query fills the evolved-species default. Phase 3A: every returned team carries a `cost_breakdowns` slice aligned with `members`, giving the per-Pokémon stardust climb from its current level to a shared target (omit / 0 = per-species deepest level fitting the league CP cap at 15/15/15 IVs — the "max powerup without busting cap" target; positive value = exact 0.5-grid level; if the member's current level is already at or above the resolved target the powerup cost clamps to zero with `already_at_or_above_target=true`) plus the second-move unlock cost (stardust + candy). Options multipliers (shadow ×1.2 / purified ×0.9 / lucky ×0.5 stardust-only) applied. Over-target members clamp to zero with `already_at_or_above_target=true`. Pool members whose level-1 CP already exceeds the league cap fail fast with `ErrMemberInvalidForLeague` so the client can fix the pool rather than getting a partial run. Powerup-candy is NOT emitted (same cross-source-disagreement rationale as `pvp_powerup_cost`); second-move candy IS emitted because the buddy-distance derivation is unambiguous. Optional `budget` block (`stardust` + `stardust_tolerance` today; `elite_charged_tm` / `elite_fast_tm` / `xl_candy` / `rare_candy_xl` accepted but not yet enforced) filters teams whose summed powerup + second-move stardust exceeds the limit. Teams within `limit × (1 + tolerance)` are kept with `budget_exceeded=true` + `budget_excess=<overBy>`; teams beyond that band drop. Under-budget teams still get `aggregate_stardust_cost` populated so callers see the total; the value is the sum of powerup + second-move stardust over the team's three members, candy / XL-candy / ETM inventory is NOT rolled in (those live per-member in `cost_breakdowns`). R5 finding #5: branching auto_evolve skips now surface `auto_evolve_alternatives` on the member's cost breakdown — each entry carries the child species id, its predicted CP at the member's current level, and whether the child's level-1 floor fits the league cap. R5 finding #6: every response carries a top-level `pool_members` array — one row per input pool entry with `index`, `original_species`, `resolved_species`, `auto_evolve_action` (`kept` / `evolved` / `skipped_branching` / `skipped_over_cap` / `skipped_unrecognised`), `banned` flag, and `in_returned_team` bool. Lets callers diagnose "why didn't my Togetic land in any team" without re-reading the per-member flags.
- `pvp_species_info` — read-only lookup: base stats, full fast/charged move lists with per-move legacy flags, evolution chain (plus pre-evolution parent), tags, released flag, and a best-effort overall rank across the four standard leagues.
- `pvp_move_info` — read-only lookup: type, power, energy, duration, plus a reverse-index of every species on which this move is flagged legacy.
- `pvp_type_matchup` — compute the damage multiplier a move type deals to a defender with the given type list; returns the composite number plus a human-readable breakdown (`"grass vs water(1.60) × ground(1.60) = 2.56"`).
- `pvp_level_from_cp` — given species + IVs + observed CP, invert back to the highest level (on the 0.5 grid) that fits under that CP; returns the resolved stats so clients don't need a second `pvp_rank` call.
- `pvp_counter_finder` — given a target (species + IV + optional moveset), find the top-N counters. Accepts an optional `from_pool` to scan the caller's box; omit it to scan the top-N pvpoke meta for the cup instead. Returns per-counter battle rating, per-shield-scenario breakdown, and remaining HP.
- `pvp_evolution_preview` — given current species + IVs + observed CP, invert CP to level and project each reachable descendant form's stats at that same level (evolution preserves level). Returns CP, stat line, and the subset of standard leagues (little / great / ultra / master) each evolved form fits under. Supports branching chains (eevee → vaporeon/jolteon/etc.) and multi-hop paths.
- `pvp_rank_batch` — score the same species + league under many IV triples in one call. Response carries one `RankBatchEntry` per input IV in order (OK / error / RankResult) plus a top-level `success_count`. Capped at 64 IVs per call to bound server work.
- `pvp_threat_coverage` — given a 3-member team and a candidate pool, identify meta species the team does not cover (best-of-team rating < `uncoveredThreshold=400`) and, for each uncovered threat, surface up to 3 pool members whose averaged rating crosses the same threshold, sorted descending. Ratings averaged across the requested shield scenarios.
- `pvp_weather_boost` — static lookup of Pokémon GO's weather → boosted-types table. Pass an empty `weather` to get all seven conditions (sunny / rainy / partly_cloudy / cloudy / windy / snow / fog) and their boosted types, or a specific name for one row. Response includes the `1.2×` PvE damage bonus as reference data; weather boost is **not applied in PvP / GO Battle League** — the battle simulator engine ignores it. Case-insensitive input.
- `pvp_encounter_cp_range` — given a species id, report min / max CP for each canonical Pokémon GO encounter source (wild spawns unboosted / boosted, research, raids unboosted / boosted, GBL rewards, egg hatches, Team GO Rocket grunt shadow). Each row carries the pinned level (or level range) and the IV floor (e.g. raids lock IVs to 10..15; weather-boosted raids bump the caught level from 20 to 25).
- `pvp_cup_rules` — look up the include / exclude filter rules + PartySize / LevelCap overrides for each pvpoke cup (`all`, `spring`, `jungle`, ...). Filter types surface raw (`type` / `tag` / `id` / `evolution`) so clients can reason about cup membership without a second tool call. Pass an empty `cup` for the full table.
- `pvp_second_move_cost` — per-species stardust + candy cost to unlock a second charged move. Stardust from gamemaster `thirdMoveCost`; candy derived from buddy distance (1km → 25, 3km → 50, 5km → 75, 20km → 100). Modifier flags via `options`: `shadow=true` → ×1.2 both currencies (resolves the `_shadow` gamemaster entry internally), `purified=true` → ×0.9 both, `lucky=true` → no effect (Niantic's 50% discount is powerup-only). Flags stack. Shadow + missing pvpoke variant reports `shadow_variant_missing=true` with fallback to base species.
- `pvp_powerup_cost` — sum stardust over a full L1-L50 powerup climb in 0.5-level steps (pre-XL L1 → L40 = 78 steps = 270,000 stardust; XL era L40 → L50 = +20 steps = +250,000; full climb = 520,000). `options.shadow=true` applies ×1.2 stardust, `options.purified=true` ×0.9, `options.lucky=true` ×0.5 (Niantic's Lucky Pokémon discount is stardust-only). Flags stack multiplicatively — integer arithmetic keeps every single-flag result exact on the canonical stardust tiers. Candy is not returned in this phase: Bulbapedia's L1-L50 candy table is self-consistent (304 regular candy L1→L40, 296 XL candy L40→L50) but other publicly-cited sources (mathiasbynens, older GamePress pages) publish different per-bucket numbers, and we do not ship a candy figure before cross-source agreement is verified. A dedicated follow-up branch will add candy once the audit is done. `crosses_xl_boundary` + `xl_steps_included` flag how many of the summed steps fall in the XL-candy era for callers doing separate XL-candy budget planning.
- `pvp_pokedex_lookup` — find species in the current gamemaster by pokedex number, exact species id, or case-insensitive substring of species id or display name. Dispatches on query shape: all-digit → dex match; exact species id → single row first; otherwise → case-insensitive substring scan against id + name. Shadow variants excluded by default (`options.shadow=true` on battle tools is the canonical way to address shadow forms); pass `include_shadow=true` to surface them. Results capped at 10; narrow the query if more than 10 species match. Empty / whitespace-only query rejected with `ErrEmptyPokedexQuery` rather than dumping the entire ~1700-species gamemaster.
- `pvp_evolution_target` — reverse-lookup for powerup planning: given the DESIRED evolved species and a league, walk `PreEvolution` back to the chain root and sweep every IV triple to find the one producing the maximum root-species CP at the winning evolved level, while target still clears `target_percent_of_best` (default 95) of the best legal spread under the league cap. Returns `from_species` (chain root), `chain_from_to` (root → ... → target), `max_root_cp_at_evolved_level` (root CP **at the evolved level** — NOT a wild-catch CP ceiling; for Ultra / Master this may exceed anything a freshly-caught root can display), `evolved_level` (the 0.5-grid level of the winning spread — same for root and target since evolution preserves level), `typical_wild_cp_range_unboosted` ([0/0/0 at L1, 15/15/15 at L30] — the realistic wild-catch CP space), `percent_of_best_at_max`, `best_stat_product`, and a terse `evolution_hint` describing the catch-and-evolve path (deliberately tool-name-free; callers should consult their preferred data source for per-step candy / item requirements). Rejects target species without a `PreEvolution` with `ErrNotInEvolutionChain` (catching a terminal species directly is a caller mistake). When the gamemaster snapshot is missing an ancestor species (drift between cached snapshot and current pvpoke data) the walk terminates at the last known species; the caller can detect the truncation by observing `from_species == target_species`. Shadow-aware lookup honours `options.shadow=true`. `cup` accepted for API symmetry but not currently enforced — the IV / stat-product math is cup-agnostic.
- `pvp_report_data_issue` — zero-dependency escalation pointer. Returns the GitHub repository URL, new-issue URL, and a checklist of information a good data-accuracy bug report should include (tool name, exact input, observed output, expected output with source citation, observation date). Intended for callers or their human operators who spot a mismatch between a tool response and an authoritative source (Bulbapedia, in-game display, Niantic patch notes) — several tools carry hardcoded tables that can drift when Niantic adjusts mechanics, and the issue tracker is the primary correction channel.
- `diff-gm` (CLI-only, not an MCP tool) — diff the upstream gamemaster against the local cache. Exits non-zero on any difference so cron / CI can alert on unexpected drift. See "Gamemaster drift" below.

Most MCP tools that consult pvpoke rankings accept an optional `cup` parameter naming a pvpoke cup (`spring`, `retro`, `jungle`, ...); empty resolves to the open-league `all` rankings. 404s on unsupported (cup, cap) pairs surface as `ErrUnknownCup` rather than silently falling back. Exceptions: `pvp_rank` and `pvp_rank_batch` do NOT accept `cup` — they return the species' position in every cup as `rankings_by_cup` in one call, removing the per-cup drift that made the old `cup` input misleading (only moveset honoured it; `percent_of_best` stayed open-league).

Twelve tools accept an optional `options` block with `shadow` / `lucky` / `purified` booleans (Phase X refactor, Phase X-II migration): `pvp_rank`, `pvp_rank_batch`, `pvp_species_info`, `pvp_level_from_cp`, `pvp_cp_limits`, `pvp_evolution_preview`, `pvp_matchup`, `pvp_team_analysis`, `pvp_team_builder`, `pvp_counter_finder`, `pvp_threat_coverage`, `pvp_second_move_cost`. `pvp_encounter_cp_range` is the deliberate exception — encounter sources (wild spawns, raids, research, hatches) never produce shadow variants, so an Options block there would be meaningless; Team GO Rocket shadow encounters have their own dedicated row in the response. `options.shadow=true` is the new way to address shadow variants — the old `species: "medicham_shadow"` suffix convention still works, and mixing the two (`species: "medicham_shadow"` + `options.shadow=true`) is tolerated via suffix stripping. When pvpoke has not yet published the shadow row for a species, the response carries `shadow_variant_missing=true` and falls back to the base species. The battle simulator applies in-game shadow ATK×1.2 / DEF÷1.2 multipliers to damage math (Phase R4.7) via `pogopvp.Combatant.IsShadow`, so shadow matchups produce different HP / turn counts than the non-shadow mirror; `options.shadow` also drives legacy-move resolution, moveset auto-fill, species lookup, and cost estimation. `options.lucky` and `options.purified` are accepted on every migrated tool for struct-level symmetry but are load-bearing only on `pvp_second_move_cost` (purified ×0.9 both currencies; lucky is a no-op here since Niantic's 50% stardust discount is powerup-only).

No tagged release exists yet. The GitHub repository rename from `pvpoke-mcp` to `pogo-pvp-mcp` is pending, so `go install github.com/lexfrei/pogo-pvp-mcp/cmd/pogo-pvp-mcp@latest` does not yet resolve.

## Running locally

The companion engine module lives in a sibling directory during early development and is wired through a `replace` directive in `go.mod`:

```text
replace github.com/lexfrei/pogo-pvp-engine => ../pogo-pvp-engine
```

Clone both repos side-by-side under the same parent, then:

```bash
go build ./cmd/pogo-pvp-mcp
./pogo-pvp-mcp fetch-gm    # warm the local cache from upstream
./pogo-pvp-mcp serve       # run over MCP stdio
```

Configuration flows through `--config path/to/config.yaml` (optional) and the `POGO_PVP_*` environment prefix. `POGO_PVP_CONFIG` is honoured as the default for `--config`, so you can set it once in your shell instead of repeating the flag. There is no XDG or standard-path config lookup — either `--config`, `POGO_PVP_CONFIG`, or env overrides + hard-coded defaults.

Two filesystem caches live alongside each other by default:

- `$XDG_CACHE_HOME/pogo-pvp-mcp/gamemaster.json` — the upstream pvpoke gamemaster, refreshed every 24h or forced via `fetch-gm`.
- `$XDG_CACHE_HOME/pogo-pvp-mcp/rankings/{cup}/{role}/rankings-{500,1500,2500,10000}.json` — per-(cup, role, league) pvpoke rankings, fetched lazily the first time a meta-driven tool (`pvp_meta`, `pvp_team_analysis`, `pvp_team_builder`) touches that triple. Each file expires after 24h and is re-fetched on the next access. `{cup}` is `all` when no cup is requested; current pvpoke cups include `spring`, `retro`, `jungle`, `bayou`, `maelstrom`, `spellcraft`, `fantasy`, `premier`, `championship`, `naic2026`, `laic2025remix`, `catch`, `chrono`, `classic`, `electric`, `equinox`, `battlefrontiermaster`, `bfretro`, `gobattleleague`, `little` — any id pvpoke publishes under `src/data/rankings/{id}/` is accepted. `{role}` is `overall` for the default slice consumed by the tools; `pvp_meta` additionally pulls `leads`, `switches`, and `closers` to classify each species. Not every (cup, cap) pair exists upstream (Spring Cup only publishes at 1500); the manager surfaces `rankings.ErrUnknownCup` when upstream returns 404.

## Gamemaster drift

`pogo-pvp-mcp diff-gm` compares the upstream pvpoke gamemaster against the local cache and prints a human-readable report of added / removed / changed species and moves. It does **not** mutate the cache. Exits `0` on a clean diff, `1` on any drift — drop it in a cron or CI job to catch balance patches the moment they land:

```bash
pogo-pvp-mcp diff-gm           # fetch upstream, diff vs local cache
pogo-pvp-mcp diff-gm --against /path/to/older-gamemaster.json
```

Use `--against` to diff two on-disk snapshots without touching the network — useful for historical comparisons after `fetch-gm` has already overwritten the cache.

## Debug HTTP surface

Setting `server.http_port` (or `POGO_PVP_SERVER_HTTP_PORT`) to a non-zero port opens a small debug surface on top of the MCP stdio transport:

- `GET  /healthz` — 200 when the gamemaster is loaded, 503 otherwise.
- `POST /refresh` — synchronous upstream gamemaster refresh.
- `GET  /debug/gamemaster` — Pokémon / move counts + version string.

It binds `127.0.0.1` by default; override via `server.http_host` if you need to expose it externally (don't — it's intended for local readiness probes and on-demand cache primes).

## Public MCP HTTP listener

Setting `server.mcp_http_listen` (or `POGO_PVP_SERVER_MCP_HTTP_LISTEN`) to a non-empty Go listen-address opens a second, **public-facing** HTTP endpoint that speaks Streamable HTTP MCP. Intended for remote LLM clients that don't run a local stdio proxy.

```bash
POGO_PVP_SERVER_MCP_HTTP_LISTEN=0.0.0.0:8080 ./pogo-pvp-mcp serve
# → both stdio transport AND http://<host>:8080 accept MCP requests
```

Behaviour:

- **Opt-in**: empty / unset = disabled; stdio remains the only transport.
- **Stateless**: each request is self-contained (`Stateless:true` + `JSONResponse:true` on the SDK handler); no session state across requests. Fine for read-only data.
- **Timeouts**: ReadHeader 5s, Read 30s, Write 60s, Idle 90s, MaxHeaderBytes 64 KiB. Graceful shutdown drains in 60s on `SIGTERM`.
- **Separate from debug**: the loopback debug server (`server.http_port`) stays on `127.0.0.1` with its auth-free `/healthz` / `/refresh` endpoints. The two listeners are orthogonal.

Phase 3 adds the net/http middleware chain around the MCP handler (order outer → inner: `recover → securityHeaders → realIP → rateLimit → maxBytes`). `securityHeaders` always runs with the Phase 5 baseline set (see "Phase 5 security headers" below); the remaining layers are configurable:

| Field (env var) | Default | 0 / empty means |
| --- | --- | --- |
| `server.trusted_proxies` (`POGO_PVP_SERVER_TRUSTED_PROXIES`) | `[]` (trust nobody) | `X-Forwarded-For` always ignored; `RemoteAddr` is the client |
| `server.rate_limit_rps` (`POGO_PVP_SERVER_RATE_LIMIT_RPS`) | `10` requests/sec per client IP | `0` disables rate limiting entirely — dev only |
| `server.rate_limit_burst` (`POGO_PVP_SERVER_RATE_LIMIT_BURST`) | `20` burst budget | `0` silently clamped to `1` when RPS > 0 — `burst=0` would reject every first request |
| `server.max_request_bytes` (`POGO_PVP_SERVER_MAX_REQUEST_BYTES`) | `65536` (64 KiB) | `0` disables the body cap — dev only |

**Phantom rate-limit pitfall**: when `trusted_proxies` covers the proxy IP but the proxy does NOT forward an `X-Forwarded-For` header (or all XFF entries are themselves trusted), every downstream user collapses to a single rate-limit bucket keyed on the proxy IP. Symptom: legitimate traffic rate-limits itself long before the configured RPS. Fix: configure your reverse proxy to set / forward `X-Forwarded-For` on requests to the MCP endpoint.

Every response also carries the Phase 5 baseline security headers: `Strict-Transport-Security` (1-year HSTS), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Content-Security-Policy: default-src 'none'`. TLS itself is terminated at the reverse proxy; HSTS is the browser-side reinforcement.


**DNS-rebinding protection (SDK built-in)**: the MCP SDK rejects requests that arrive via a loopback listener (`127.0.0.1`, `[::1]`) with a non-loopback `Host` header — a 403 is returned. When binding to `127.0.0.1` for local development, keep the client's `Host` header as `127.0.0.1:PORT` (or drop it). Proxied deployments where `Host` matches the public FQDN and the listener is on `0.0.0.0` are unaffected.

## MCP-level controls (stdio AND HTTP)

These wrappers apply to every MCP method on the server regardless of transport — Claude Desktop over stdio gets the same coverage as a remote LLM over HTTP.

- **Per-method timeout**: heavy methods (`pvp_team_builder`, `pvp_team_analysis`, `pvp_threat_coverage`, `pvp_counter_finder`, `pvp_rank_batch`) get `server.tool_timeout_heavy` (default `30s`); everything else gets `server.tool_timeout_default` (default `5s`). Zero/negative disables the wrapper for that tier.
- **Structured logging**: every call emits `mcp method ok` or `mcp method failed` with `method`, `tool`, `duration_ms`, and on failure `error` + `timed_out` (true iff the error wraps `context.DeadlineExceeded`). Honours the server's `POGO_PVP_LOG_FORMAT` (text|json).

Env vars: `POGO_PVP_SERVER_TOOL_TIMEOUT_DEFAULT`, `POGO_PVP_SERVER_TOOL_TIMEOUT_HEAVY`.

## Claude Desktop integration

Add the server to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

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

Restart Claude Desktop. The twenty-two `pvp_*` tools will appear in the tool list. If a tool returns "gamemaster not loaded", run `pogo-pvp-mcp fetch-gm` once to warm the cache.

## Container image

A `Containerfile` ships in the repo root; tagged builds produce multi-arch (linux/amd64 + linux/arm64, cosign-signed) images at `ghcr.io/${GITHUB_REPOSITORY}:vX.Y.Z`. Until the GitHub repo is renamed from `pvpoke-mcp` to `pogo-pvp-mcp`, the effective image coordinate is `ghcr.io/lexfrei/pvpoke-mcp:vX.Y.Z`; after the rename it flips to `ghcr.io/lexfrei/pogo-pvp-mcp:vX.Y.Z` without any workflow change (the release workflow reads `${{ github.repository }}`).

The container `EXPOSE`s two ports: `8080` for the public MCP HTTP endpoint (enable via `POGO_PVP_SERVER_MCP_HTTP_LISTEN=:8080`) and `8787` for the debug surface (`server.http_port`; keep on the container-internal loopback, never map to the host's public interface).

Note: the image build depends on `github.com/lexfrei/pogo-pvp-engine` being resolvable by `go mod download` — during the engine-sibling development window (while the `replace` directive in `go.mod` points at a local `../pogo-pvp-engine` checkout), the Containerfile will not build cleanly. It becomes buildable once the engine repository is published and tagged.

## Public deployment (reverse proxy example)

Example nginx fragment for terminating TLS at a proxy and forwarding to the container's MCP HTTP endpoint. `trusted_proxies` on the MCP side must cover the proxy's IP so `X-Forwarded-For` is honoured; `X-Forwarded-For` MUST be forwarded by the proxy (otherwise every downstream user collapses into one rate-limit bucket — see "Phantom rate-limit pitfall" above):

```nginx
server {
    listen 443 ssl http2;
    server_name mcp.example.com;

    ssl_certificate     /etc/letsencrypt/live/mcp.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mcp.example.com/privkey.pem;

    location / {
        proxy_pass         http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
        proxy_read_timeout 90s;
    }
}
```

Matching MCP env:

```bash
POGO_PVP_SERVER_MCP_HTTP_LISTEN=127.0.0.1:8080
POGO_PVP_SERVER_TRUSTED_PROXIES=127.0.0.0/8
POGO_PVP_SERVER_RATE_LIMIT_RPS=10
POGO_PVP_SERVER_RATE_LIMIT_BURST=20
POGO_PVP_LOG_FORMAT=json
```

## Disclaimer

This project is not affiliated with, endorsed by, or sponsored by Niantic, Inc., Nintendo, The Pokémon Company, Game Freak, or Creatures Inc. "Pokémon" and related names are trademarks of their respective owners.

The server operates exclusively on factual game data (stat lines, movesets, CPM values) fetched from the open-source [PvPoke][pvpoke] project (MIT licensed). No artwork, sprites, or audio is distributed. Pokémon are identified by string id only.

## Roadmap

- Full battle-simulation-based ranker (engine-side) so `pvp_meta` stops depending on pre-computed pvpoke JSONs.
- CMP (charge-move-priority) scaling in the battle engine. (Shadow ATK × 1.2 / DEF ÷ 1.2 already landed in Phase R4.7.)

## License

BSD 3-Clause. See [LICENSE](LICENSE).

[pvpoke]: https://github.com/pvpoke/pvpoke
