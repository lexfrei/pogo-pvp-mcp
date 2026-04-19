# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Module and repository layout

- Go module: `github.com/lexfrei/pogo-pvp-mcp`. The on-disk directory is still `pvpoke-mcp` — a `gh repo rename` to `pogo-pvp-mcp` is pending.
- This module depends on a sibling repo, `github.com/lexfrei/pogo-pvp-engine`, which holds all PvP math (battle sim, IV finder, stats, type chart). During local development `go.mod` has `replace github.com/lexfrei/pogo-pvp-engine => ../pogo-pvp-engine`, so both repos must be checked out side-by-side:
  - `~/git/github.com/lexfrei/pogo-pvp-mcp/`
  - `~/git/github.com/lexfrei/pogo-pvp-engine/`
- The `replace` directive is removed once the engine repo is published and tagged. Until then, the `Containerfile` does not build cleanly (it calls `go mod download`, which fails on the unpublished engine) — this is documented in the README and expected.

## Common commands

```bash
go build ./cmd/pogo-pvp-mcp              # binary -> ./pogo-pvp-mcp
go test ./... -race -count=1             # full suite, no cache, race detector
go test ./internal/tools/ -run TestRank  # single package, single test by prefix
golangci-lint run                        # matches CI; config is strict (see below)

go run ./cmd/pogo-pvp-mcp fetch-gm       # populate gamemaster cache from pvpoke
go run ./cmd/pogo-pvp-mcp serve          # MCP server over stdio
POGO_PVP_SERVER_HTTP_PORT=8787 go run ./cmd/pogo-pvp-mcp serve   # + debug HTTP
```

`POGO_PVP_CONFIG` acts as the default for `--config`; all other config flows via `POGO_PVP_*` env or `--config path/to.yaml` (there is no XDG config-file lookup).

## Linter configuration is strict — read `.golangci.yml` before fighting it

`linters.default: all` with a small disable list. Surprises that bite often:

- `varnamelen` rejects short names in non-trivial scope. The `ignore-decls` allowlist includes `iv IV`, `rt *Runtime`, plus the usual `wg`/`mu`/`ok`. Add new short-name exemptions there rather than renaming a domain-standard abbreviation.
- `noinlineerr` forbids the `if err := foo(); err != nil` pattern in non-test code. Use `err := foo(); if err != nil`.
- `gocritic.hugeParam` fires on anything ≥~70 bytes. The MCP SDK's `ToolHandlerFor[In, Out]` signature requires **value** receivers, not pointers, so `internal/tools/` has a path exemption; don't generalise it to other packages.
- `gochecknoglobals` is on — domain-constant tables (CPM, type-effectiveness) use `//nolint:gochecknoglobals` with an explanatory comment.
- `mnd` flags bare numeric literals. League CP caps, shield thresholds, rating midpoints etc. all have named `const`s.
- `funlen` cap is 50 lines; split handlers once they grow past that. The `handle` methods in `internal/tools/` typically delegate to `resolveXInputs` + `buildXResult` helpers for this reason.
- `tagliatelle` expects camelCase JSON tags by default — `internal/tools/` and `internal/debug/` have path exemptions for the MCP snake_case convention.

## Architecture in one pass

The MCP server is a thin wrapper around the engine. The hot shape: an MCP tool handler pulls the current gamemaster + rankings snapshots from two long-lived managers, resolves user-facing strings (species ids, move ids, league names) into engine types, calls into `pogo-pvp-engine`, and packages the result as JSON.

### Package layout (under `internal/`)

- `cli/` — cobra command tree (`serve`, `fetch-gm`), `Runtime` struct that flows through context, background-loop orchestration (gamemaster refresh, optional debug HTTP server), graceful-shutdown plumbing.
- `config/` — viper-based `Config.Load` with defaults → YAML → `POGO_PVP_*` env precedence, plus `Validate` split across `validateServerAndLog` / `validateDataPlane` so gocyclo stays under 10.
- `logging/` — slog setup (text/json handler), separate from config because tests inject a `bytes.Buffer`.
- `gamemaster/` — `Manager` wraps `pogopvp.ParseGamemaster` over an HTTP fetcher with ETag conditional requests and temp-then-rename atomic cache writes. `Current()` is `nil` before the first successful load; tools check for it explicitly and return `ErrGamemasterNotLoaded`.
- `rankings/` — `Manager` with per-CP-cap lazy fetch, 24h TTL on the on-disk cache (mtime-checked), and per-cap singleflight mutex so concurrent first-time `Get` calls coalesce into a single upstream HTTP request.
- `cache/` — byte-capped LRU ready to memoise tool responses. **Not currently wired into the handlers** — see the package doc.
- `debug/` — optional HTTP surface (`/healthz`, `POST /refresh`, `/debug/gamemaster`) served when `server.http_port` is non-zero.
- `tools/` — one file per MCP tool plus shared helpers. `RankParams` / `RankResult` etc. carry the JSON contract, `NewXTool(...)` constructs the tool, `.Tool()` + `.Handler()` feed `mcp.AddTool`.

### MCP tools

1. `pvp_rank` — CP, stat product, percent-of-best for a given species at a given IV/level under a league cap.
2. `pvp_matchup` — 1v1 simulation via `pogopvp.Simulate`; returns winner/turns/HP/energy/shield counts.
3. `pvp_cp_limits` — highest level and CP a species with given IVs can reach under each standard league cap (Little/Great/Ultra). Honours the same `XL` flag as `pvp_rank`; Master is omitted because its cap is unreachable.
4. `pvp_meta` — trimmed top-N rankings slice from pvpoke's pre-computed JSON.
5. `pvp_team_analysis` — simulates team × top-N-meta, aggregates per-member rating, coverage matrix, uncovered threats.
6. `pvp_team_builder` — enumerates C(pool, 3) triples from a candidate pool, scores each against meta, returns top-K.

### Non-obvious invariants (you will break these)

- **Meta combatants use level cap 50, not 40.** `rankingsMaxLevelCap = 50` in `team_analysis.go` must match the `levelCap` pvpoke used when generating the rankings JSON we consume. Don't "helpfully" lower it to NoXLMaxLevel.
- **Shields field is `[]int`, not `[2]int`.** On `TeamAnalysisParams` / `TeamBuilderParams` an empty/nil slice means "use the `[1, 1]` default"; an explicit `[0, 2]` is honoured literally. `MatchupParams.Shields` is still `[2]int` with a different convention (zero-value = `[0, 0]` shields) — this inconsistency is documented in their godocs and should not be "fixed" by flattening both.
- **Required species are deduplicated by id, not by pool index.** A pool containing two variants of species "a" with `Required: ["a"]` must produce triples containing at least one "a" — never forced to contain both. `resolveRequired` returns a `map[string]struct{}`; `containsAllSpecies` checks membership.
- **Rating-matrix failures are excluded from averages, not treated as ties.** In `team_analysis.analyzeMember` a failed `ratingFor` increments `Failures` and `continue`s — `AvgRating` divides by `len(meta) - failures`. In `team_builder.evaluateTeams` the precomputed matrix carries an `OK` bool per cell; `scoreTripleFromMatrix` skips `!OK` cells and divides by actual sample count. If you add new aggregators, follow the same rule — a silent `500` midpoint fallback is a bug, not a policy.
- **`team_builder` precomputes the pool × meta rating matrix once.** Don't call `ratingFor` inside the triple-enumeration loop. The matrix pattern turns O(pool³ × meta) simulations into O(pool × meta) — regressing this is a quadratic perf bomb on realistic pool sizes.
- **`MaxPoolSize = 50` is a DoS guard** against LLM-supplied huge pools. Reject with `ErrPoolTooLarge` before any enumeration.
- **`Combatant.Valid()` is the source of truth for simulation preconditions.** `Simulate` calls it on both sides and wraps the first failure with `ErrInvalidCombatant`. Don't add parallel validation in `internal/tools/` — defer to the engine.
- **Tool handlers honour `ctx.Err()` at loop boundaries, not just on entry.** `runTeamAnalysis` and `evaluateTeams` check between each outer iteration and the handler re-checks after the sweep returns. A client disconnect during a multi-million simulation budget must release the worker goroutine.
- **`rankings.Manager.Get` is singleflight per (cup, cap) pair.** The Get signature is `Get(ctx, cap int, cup string)` — cup="" normalises to "all". Don't bypass it with direct `loadLocal` / `fetchUpstream` calls; you'll reintroduce the cold-start thundering-herd. Upstream 404 for unsupported (cup, cap) pairs wraps `ErrUnknownCup`, not `ErrUpstreamStatus`.
- **Neither README nor package docs should claim a tool behaviour that isn't wired.** Past review rounds flagged doc drift as blocking (e.g. describing the LRU cache as "memoising tool responses" when the handlers don't touch it). If you change behaviour, update `README.md` + relevant package doc in the same commit.

### Testing conventions

- Table-driven tests with `t.Parallel()` per case (subtests also parallel).
- Shared test-only helpers (`newManagerWithFixture`, `newTeamAnalysisTool`, `mustSimulate`) live in the `_test.go` files alongside their users.
- Fixture JSON for gamemaster / rankings is inlined as raw strings at the top of the test file. `testdata/gamemaster_sample.json` exists only for the engine repo — this repo builds fixtures per-test.
- Integration tests in `internal/cli/integration_test.go` wire a real `mcp.Server` and client over `mcp.NewInMemoryTransports`. `TestIntegration_ListTools` must list every registered tool; if you add or remove a tool in `buildMCPServer`, update the expected-tools slice.

## Known limitations (roadmap, not bugs)

- The `pvp_meta` / `pvp_team_analysis` / `pvp_team_builder` tools depend on pvpoke's pre-computed rankings JSON. A full engine-side ranker would eliminate that dependency.
- Battle simulator does not model Charge-Move-Priority or shadow atk/def multipliers; charged throws resolve after fast damage on the shared tick. Documented on `Simulate`'s godoc.
- `team_builder` is single-threaded. A worker-pool version is planned once the quadratic work is bounded (already helped by the rating matrix).
