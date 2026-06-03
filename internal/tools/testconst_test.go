package tools_test

// Shared domain constants for the external (`tools_test`) test suite.
// These pvpoke move ids, species ids, synthetic fixture ids, and JSON
// field names recur across many test files; declaring them once keeps
// goconst satisfied and a typo in one place from silently diverging
// from the rest of the suite. Move / species ids already declared in
// individual files (moveCounter, moveIcePunch, movePsychic,
// moveAquaTail, speciesMedicham, speciesQuagsire) are reused, not
// redeclared.

// pvpoke move ids used across multiple test files.
const (
	moveCrossChop    = "CROSS_CHOP"
	moveBubble       = "BUBBLE"
	moveMudShot      = "MUD_SHOT"
	moveStoneEdge    = "STONE_EDGE"
	moveHydroPump    = "HYDRO_PUMP"
	moveDynamicPunch = "DYNAMIC_PUNCH"
	movePsychoCut    = "PSYCHO_CUT"
	moveNotAMove     = "NOT_A_MOVE"
	moveIceBeam      = "ICE_BEAM"
)

// pvpoke species ids used across multiple test files.
const (
	speciesAzumarill   = "azumarill"
	speciesDragonite   = "dragonite"
	speciesAlakazam    = "alakazam"
	speciesFarigiraf   = "farigiraf"
	speciesDitto       = "ditto"
	speciesColossus    = "colossus"
	speciesMachamp     = "machamp"
	speciesMissingno   = "missingno"
	speciesMarshmallow = "marshmallow"
	speciesPhantom     = "phantom"
	speciesPorygon     = "porygon"
	speciesGloom       = "gloom"
)

// League-name and pvpoke type-name strings used across test files.
const (
	leagueMaster = "master"
	leagueUltra  = "ultra"
	typeGrass    = "grass"
	typeGround   = "ground"
	typeRock     = "rock"
	typeWater    = "water"
)

// Weather-query names used by the weather-boost tests.
const (
	weatherSunny = "sunny"
	weatherRainy = "rainy"
)

// Cup id and synthetic / shadow species ids used by individual tests.
const (
	cupSpring            = "spring"
	speciesScytherShadow = "scyther_shadow"
	speciesUnknownBase   = "unknownbase"
	speciesTinyBase      = "tinybase"
)

// JSON field names asserted in the powerup-cost response shape.
const (
	fieldSteps              = "steps"
	fieldToLevel            = "to_level"
	fieldStardustCost       = "stardust_cost"
	fieldStardustMultiplier = "stardust_multiplier"
)

// Encounter-source ids exercised by encounter_cp_range tests.
const (
	encounterRaid     = "raid"
	encounterHatchEgg = "hatch_egg"
)

// Loopback / reserved URLs used by the not-loaded-manager and
// fetch-failure test paths.
const (
	urlExampleInvalid = "http://example.invalid"
	urlLoopback       = "http://127.0.0.1:1"
)

// Misc recurring free-text labels used in table-driven cases.
const (
	labelNegative      = "negative"
	labelOffGridTarget = "off-grid target"
	fieldNote          = "note"
	labelIssue         = "issue"
)

// Synthetic move / species ids used by the team-builder and
// team-analysis fixtures.
const (
	moveFast1    = "FAST1"
	moveCharged1 = "CH1"
	moveChSquirt = "CH_SQUIRT"
	speciesBulk  = "bulk_species"
)

// Shield-scenario keys in TeamAnalysisResult.PerScenario.
const (
	scenario0s = "0s"
	scenario1s = "1s"
	scenario2s = "2s"
)

// JSON field names asserted in the powerup-cost response shape.
const (
	fieldFromLevel        = "from_level"
	fieldBaselineStardust = "baseline_stardust_cost"
	fieldCostMultiplier   = "cost_multiplier"
	fieldCandy            = "candy"
)

// Misc recurring test labels / type names.
const (
	labelAboveL50 = "above L50"
	typeFairy     = "fairy"
)
