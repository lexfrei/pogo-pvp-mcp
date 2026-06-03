package cli_test

// Shared string constants for the cli_test package. Hoisted so the
// repeated tool names, command names, MCP arg keys, client-identity
// strings, and HTTP security-header names referenced across the
// external test files resolve to a single symbol (goconst trigger —
// these are all the same cli_test namespace).

// CLI subcommand names exercised through the cobra root command.
const (
	cmdFetchGM = "fetch-gm"
	cmdDiffGM  = "diff-gm"
)

// MCP tool names advertised by the wired server. Aligned verbatim
// across integration_test.go, http_transport_test.go, and
// doc_drift_test.go so a dropped or renamed tool surfaces identically
// everywhere.
const (
	toolRank             = "pvp_rank"
	toolMatchup          = "pvp_matchup"
	toolCPLimits         = "pvp_cp_limits"
	toolMeta             = "pvp_meta"
	toolTeamAnalysis     = "pvp_team_analysis"
	toolTeamBuilder      = "pvp_team_builder"
	toolSpeciesInfo      = "pvp_species_info"
	toolMoveInfo         = "pvp_move_info"
	toolTypeMatchup      = "pvp_type_matchup"
	toolLevelFromCP      = "pvp_level_from_cp"
	toolCounterFinder    = "pvp_counter_finder"
	toolEvolutionPreview = "pvp_evolution_preview"
	toolRankBatch        = "pvp_rank_batch"
	toolThreatCoverage   = "pvp_threat_coverage"
	toolWeatherBoost     = "pvp_weather_boost"
	toolEncounterCPRange = "pvp_encounter_cp_range"
	toolCupRules         = "pvp_cup_rules"
	toolSecondMoveCost   = "pvp_second_move_cost"
	toolPowerupCost      = "pvp_powerup_cost"
	toolReportDataIssue  = "pvp_report_data_issue"
	toolPokedexLookup    = "pvp_pokedex_lookup"
	toolEvolutionTarget  = "pvp_evolution_target"
)

// MCP CallTool argument keys and a shared league value used by the
// round-trip tests.
const (
	argSpecies  = "species"
	argIV       = "iv"
	argLeague   = "league"
	leagueGreat = "great"
)

// MCP client/server identity strings shared by the in-memory and HTTP
// transport tests.
const (
	implVersionTest = "test"
	clientNameTest  = "test-client"
	clientNameHTTP  = "http-test-client"
)

// HTTP security-header names asserted in both the README doc-drift
// lock and the live middleware-chain tests.
const (
	headerHSTS = "Strict-Transport-Security"
	headerCSP  = "Content-Security-Policy"
)
