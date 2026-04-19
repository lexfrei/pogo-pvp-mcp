# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Workflow — hard rules

- **Все изменения делаются через feature-ветки.** Никогда не коммитить прямо в `master` — даже hotfix.
- **Никаких мержей без LGTM от `review-toolkit:branch-review`.** `git merge --ff-only` и `git merge --no-ff` оба запрещены до явного LGTM. Нарушение = регрессия процесса, даже если код зелёный (go test + golangci-lint не заменяют review).
- Тот же порядок распространяется на sibling-репозиторий `pogo-pvp-engine` — правки в нём тоже живут через feature-ветки, проходят `branch-review`, и только после LGTM мержатся в `engine/master`.
- Если откатываешь ошибочный merge: `git reset --hard <pre-merge-sha>`, пересоздай ветку из потерянного коммита, прогони review, жди LGTM.

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
5. `pvp_team_analysis` — simulates team × top-N-meta. Output splits into `Overall` (mean-of-means across scenarios) and `PerScenario["Ns"]` (one isolated aggregate per shield scenario); each carries the same `{team_score, per_member, coverage_matrix, uncovered_threats, simulation_failures}` shape. Scenario keys are the stringified shield counts with an `s` suffix (`"0s"`, `"1s"`, `"2s"`).
6. `pvp_team_builder` — enumerates C(pool, 3) triples from a candidate pool, scores each against meta, returns top-K. Phase 3A: every returned team carries `CostBreakdowns []MemberCostBreakdown` aligned with `Members`. Each breakdown holds the per-member stardust powerup climb from current level to a resolved target (`TargetLevel` param 0 ⇒ per-species default = deepest climb fitting the league cap at 15/15/15) + second-move stardust + candy. Options multipliers (Shadow 1.2× / Purified 0.9× / Lucky 0.5× stardust-only) applied; over-target members clamp to zero cost with `already_at_or_above_target=true`. Powerup candy NOT emitted (cross-source-disagreement rationale from `pvp_powerup_cost`); second-move candy IS emitted via buddy distance. Pre-simulation `validatePoolForLeague` rejects any pool member whose level-1 CP exceeds the league cap with `ErrMemberInvalidForLeague` — the client must fix the pool rather than getting a partial run.
7. `pvp_species_info` — read-only gamemaster lookup: base stats, move lists with per-move legacy flags, evolution chain, best-effort rank per standard league.
8. `pvp_move_info` — read-only gamemaster lookup: type/power/energy/duration plus the reverse index of every species on which this move is flagged legacy.
9. `pvp_type_matchup` — wraps `pogopvp.TypeEffectiveness` with a human-readable calculation breakdown; validates the 18 canonical pvpoke types and rejects unknowns with `ErrUnknownType`.
10. `pvp_level_from_cp` — thin wrapper over `pogopvp.LevelForCP`: given species + IVs + observed CP, return the highest 0.5-grid level that fits under the target plus the resolved stats. `Exact` distinguishes round-trip hits from CP-cap-style approximations.
11. `pvp_counter_finder` — score a pool (or the top-N meta by default) against a target combatant; returns the top-N counters sorted by averaged battle rating plus per-shield-scenario breakdown. Honours the same `disallow_legacy` gate as the team tools.
12. `pvp_evolution_preview` — invert current CP to level via `pogopvp.LevelForCP`, then BFS-walk `Species.Evolutions` to project each descendant's stats/CP at that shared level (evolution preserves level in Pokémon GO). Returns `league_fit` per evolved form, supports branching roots (eevee) and multi-hop paths. Unknown ids in the evolution chain are silently skipped to tolerate gamemaster/rankings cache skew.
13. `pvp_rank_batch` — thin batch wrapper over `pvp_rank`: same species + league + cup + CPCap + XL flag applied to every IV triple in `IVs`. Each entry is isolated (one bad IV surfaces as `OK=false` with the error message, siblings still produce results). Capped at `maxRankBatchSize=64` per call via `ErrTooManyIVs`.
14. `pvp_threat_coverage` — given a 3-member team + candidate pool, compute team coverage vs top-N meta (same averaged-rating semantics as `team_analysis`), then for each meta species below `uncoveredThreshold=400` list pool members whose rating clears the same threshold. Candidates capped at `defaultThreatCoverageCandidates=3` per threat, sorted descending by rating.
15. `pvp_weather_boost` — zero-dependency reference lookup of Niantic's weather → boosted-types table. Response includes the `1.2×` PvE damage bonus but ALWAYS carries `AppliesToPvP=false` + a `PvPNote` disclaimer — weather boost is excluded from Trainer Battles by Niantic, and the battle simulator engine does not consume it. Case-insensitive input. Returns the full seven-weather table on empty query; one-row response on named query.
16. `pvp_encounter_cp_range` — per-species min/max CP for each canonical encounter source (wild_unboosted / wild_boosted / research / raid / raid_boosted / gbl_reward / hatch_egg / rocket_shadow). Reads species from the gamemaster snapshot and applies each encounter's pinned level + IV floor via `pogopvp.ComputeCP`. Encounter rules are hardcoded from Niantic documentation; update the `encounter*Level` / `encounter*MinIV` constants when Niantic shifts a mechanic. `validateEncounterRules` runs at package init so a typo in the table panics on startup instead of silently emitting `cp=0` to clients.
17. `pvp_cup_rules` — read-only lookup over `Gamemaster.Cups` (the parsed pvpoke `cups[]` block). Each entry exposes `Include` / `Exclude` filter lists with raw `FilterType` strings (observed: `type`, `tag`, `id`, `evolution`) plus optional `PartySize` / `LevelCap` overrides. Engine extension (Phase 4A) added the `Cup` / `CupFilter` types to `pogo-pvp-engine` — bumping that dependency is required.
18. `pvp_second_move_cost` — per-species stardust + candy cost to unlock a second charged move. Stardust comes from `Species.ThirdMoveCost` (pvpoke stores only this one field). Candy is derived from `Species.BuddyDistance` via the Pokémon GO buddy-km → candy table (1 → 25, 3 → 50, 5 → 75, 20 → 100). Modifier flags live in `params.Options` (Phase X): `Shadow=true` resolves the `_shadow` gamemaster entry and applies `×1.2` stardust + candy; `Purified=true` applies `×0.9` (Bulbapedia authoritative); `Lucky=true` is a no-op here (Niantic's 50% discount is powerup-only). `scaleCost` uses integer ×12/÷10 (shadow) and ×9/÷10 (purified) to avoid float drift — every canonical value is a multiple of 5. Flags stack. `CostMultiplier` echoes the factor so callers can back it out. When shadow is requested but pvpoke doesn't publish a `_shadow` entry, `ShadowVariantMissing=true` signals fallback to base species.
19. `pvp_powerup_cost` — zero-dependency stardust summation over a half-level climb. The canonical stardust table covers the full L1-L50 range: 20 pre-XL buckets (78 half-level steps, 270,000 total) + 6 XL-era buckets (20 half-level steps, 250,000 total). Full L1→L50 climb = 520,000 stardust. `params.Options` (Phase X) applies Shadow ×1.2, Purified ×0.9, Lucky ×0.5 on stardust (Niantic's Lucky discount is stardust-only — documented on Bulbapedia's Lucky_Pokémon page). All three stack multiplicatively; `scaleStardust` uses integer arithmetic (×12/÷10, ×9/÷10, ÷2) for each single-flag case so the result is exact on every canonical tier (all multiples of 100); stacked combinations fall through to `math.Round` with float multiplication (result still integer; not guaranteed to be a multiple of 100). Candy cost is NOT returned in this phase: Bulbapedia's L1-L50 candy table is self-consistent (304 regular candy L1→L40 across 9 buckets, 296 XL candy L40→L50 across 5 buckets) but other publicly-cited sources (mathiasbynens's pogopowerupcost, older GamePress references) publish different per-bucket numbers; shipping candy before a cross-source audit risks handing callers a number that disagrees with their own reference. Deferred to a dedicated candy-cost branch. Response echoes `crosses_xl_boundary` + `xl_steps_included` so callers doing separate XL-candy budgeting can see how many of the summed half-steps fall in the XL era. Off-grid levels rejected with `ErrInvalidLevel`; above L50 rejected with the same sentinel.
20. `pvp_report_data_issue` — zero-dependency static-response tool. Takes no parameters, returns `{Message, RepositoryURL, IssuesURL, ChecklistHints}`. RepositoryURL / IssuesURL are hardcoded to `https://github.com/lexfrei/pvpoke-mcp` + `/issues/new` — tracking the **live repository name** (the Go module path is `pogo-pvp-mcp` but the `gh repo rename` is still pending; using the module path in the outbound URL returns 404). Flip both consts + `TestReportDataIssue_Payload` in one commit when the rename lands. Message explains the rationale (several tools carry hardcoded Niantic tables that drift after the table was captured), ChecklistHints enumerates the structured info a good bug report should carry (tool name, exact input, observed output, expected output with source citation, observation date). No categorisation enum — that would rot as new classes of data drift appear and the human issue triage is quick enough that structured categories add no value.

### Phase X-II — CombatantOptions on info-path tools

Phase X-II landed: `CombatantOptions` extended to the info-path tools (`pvp_rank`, `pvp_species_info`, `pvp_level_from_cp`, `pvp_cp_limits`, `pvp_evolution_preview`, `pvp_rank_batch`). Every one drives species lookup through `resolveSpeciesLookup` and accepts `Options.Shadow` / `Lucky` / `Purified`. Lucky / Purified are no-op on info-path tools — stat and CP math are shadow/purified-independent — but accepted for struct-level symmetry across the whole MCP surface (same Options struct flows through every tool). The old `_shadow` suffix convention still works (dual-convention tolerance via suffix stripping). Result naming convention unified: `Species` echoes params.Species verbatim; `ResolvedSpeciesID` carries the pvpoke gamemaster key actually used (differs only when Options.Shadow=true redirected the lookup). `pvp_encounter_cp_range` is the deliberate exception — encounter sources never produce shadow variants (Team Rocket shadows have their own response row), so an Options block there would be meaningless.

### Non-obvious invariants (you will break these)

- **Meta combatants use level cap 50, not 40.** `rankingsMaxLevelCap = 50` in `team_analysis.go` must match the `levelCap` pvpoke used when generating the rankings JSON we consume. Don't "helpfully" lower it to NoXLMaxLevel.
- **Shields field is `[]int` (team tools) vs `[2]int` (matchup).** `TeamAnalysisParams.Shields` / `TeamBuilderParams.Shields` carry a list of **symmetric shield scenarios** (each entry forces both sides to that count; `nil` / empty = `[1]`; each 0..2). `team_analysis` averages the rating across scenarios; `team_builder` uses the first scenario to seed the pool/meta combatant shields and otherwise relies on the Phase-D per-scenario rating matrix for scoring. Phase-E broke the old `[team, meta]` asymmetric semantic (pre-v0.1). `MatchupParams.Shields` is still `[2]int` per-run (zero-value = `[0, 0]`).
- **Combatant movesets are optional; FastMove drives the default.** If a `Combatant.FastMove` is empty, the battle tools resolve the recommended moveset from `rankings.Manager` using `(cpCap, cup)` and overwrite both `FastMove` and `ChargedMoves`. If `FastMove` is set but `ChargedMoves` is empty, the engine simulates fast-only (legitimate build) — no auto-fill. All battle tools echo the resolved species+moveset triple in their results (`MatchupResult.Attacker/Defender`, `TeamMemberAnalysis.FastMove/ChargedMoves`, `TeamBuilderTeam.Members` is now `[]ResolvedCombatant`, not `[]string` — breaking change pre-v0.1). `matchup.go` and `rank.go` accept a `nil` `*rankings.Manager` and fall through to "no moveset resolution"; callers in serve.go always wire in a real one.
- **Required species are deduplicated by id, not by pool index.** A pool containing two variants of species "a" with `Required: ["a"]` must produce triples containing at least one "a" — never forced to contain both. `resolveRequired` returns a `map[string]struct{}`; `containsAllSpecies` checks membership.
- **Rating-matrix failures are excluded from averages, not treated as ties.** In `team_analysis.analyzeMember` a failed `ratingFor` increments `Failures` and `continue`s — `AvgRating` divides by `len(meta) - failures`. In `team_builder.evaluateTeams` the precomputed matrix carries an `OK` bool per cell; `scoreTripleFromMatrix` skips `!OK` cells and divides by actual sample count. If you add new aggregators, follow the same rule — a silent `500` midpoint fallback is a bug, not a policy.
- **`team_builder` precomputes the pool × meta rating matrix once — per shield scenario (×3).** Don't call `ratingFor` inside the triple-enumeration loop. The matrix pattern turns O(pool³ × meta) simulations into O(pool × meta × scenarios) — regressing this is a quadratic perf bomb on realistic pool sizes. `TeamBuilderParams.OptimizeFor` selects which scenario column to score against (`overall|0s|1s|2s|all_pareto`); `all_pareto` returns the best triple per scenario plus a "best overall" (avg across scenarios), deduped by `PoolIndices`.
- **`TeamBuilderTeam.Reason` was removed; `ParetoLabel` replaces it.** JSON-breaking rename landed in Phase D — pre-v0.1, no transition period.
- **`MaxPoolSize = 50` is a DoS guard** against LLM-supplied huge pools. Reject with `ErrPoolTooLarge` before any enumeration.
- **`Combatant.Valid()` is the source of truth for simulation preconditions.** `Simulate` calls it on both sides and wraps the first failure with `ErrInvalidCombatant`. Don't add parallel validation in `internal/tools/` — defer to the engine.
- **Tool handlers honour `ctx.Err()` at loop boundaries, not just on entry.** `runTeamAnalysis` and `evaluateTeams` check between each outer iteration and the handler re-checks after the sweep returns. A client disconnect during a multi-million simulation budget must release the worker goroutine.
- **`rankings.Manager.Get` is singleflight per (cup, cap) pair.** The Get signature is `Get(ctx, cap int, cup string)` — cup="" normalises to "all". Don't bypass it with direct `loadLocal` / `fetchUpstream` calls; you'll reintroduce the cold-start thundering-herd. Upstream 404 for unsupported (cup, cap) pairs wraps `ErrUnknownCup`, not `ErrUpstreamStatus`.
- **Neither README nor package docs should claim a tool behaviour that isn't wired.** Past review rounds flagged doc drift as blocking (e.g. describing the LRU cache as "memoising tool responses" when the handlers don't touch it). If you change behaviour, update `README.md` + relevant package doc in the same commit.
- **`RankResult.Hundo` is omitted under master league.** master's cap (`10000`) saturates at `MaxLevel` for every IV, so a 15/15/15 "best-case" comparison carries no signal. Non-master leagues always populate it (defensive nil on an unreachable cap is dead code in practice — same shape as `pvp_cp_limits` fallback).
- **`MetaEntry.Role` uses a 5-position gap threshold.** `classifyRole` picks the per-role ranking where the species sits highest; it assigns that concrete role only when the next-best role's rank is ≥ 5 positions worse. Otherwise `"flex"`. A species absent from every role ranking gets `""` (field omitted). The threshold is a guess — if you change it, update `roleRankGapThreshold` and add a test case that locks the chosen value.
- **`diff-gm` must not mutate the cache.** It bypasses `gamemaster.Manager` on purpose and does a one-shot HTTP GET so a cron driver can diff against upstream without clobbering the 24h-TTL cache. An empty local cache is treated as "no baseline" (everything shows up as adds); a non-empty diff exits 1 via `cli.ErrDiffDirty`.

### Testing conventions

- Table-driven tests with `t.Parallel()` per case (subtests also parallel).
- Shared test-only helpers (`newManagerWithFixture`, `newTeamAnalysisTool`, `mustSimulate`) live in the `_test.go` files alongside their users.
- Fixture JSON for gamemaster / rankings is inlined as raw strings at the top of the test file. `testdata/gamemaster_sample.json` exists only for the engine repo — this repo builds fixtures per-test.
- Integration tests in `internal/cli/integration_test.go` wire a real `mcp.Server` and client over `mcp.NewInMemoryTransports`. `TestIntegration_ListTools` must list every registered tool; if you add or remove a tool in `buildMCPServer`, update the expected-tools slice.

## Known limitations (roadmap, not bugs)

- The `pvp_meta` / `pvp_team_analysis` / `pvp_team_builder` tools depend on pvpoke's pre-computed rankings JSON. A full engine-side ranker would eliminate that dependency.
- Battle simulator does not model Charge-Move-Priority or shadow atk/def multipliers; charged throws resolve after fast damage on the shared tick. Documented on `Simulate`'s godoc.
- `team_builder` is single-threaded. A worker-pool version is planned once the quadratic work is bounded (already helped by the rating matrix).
