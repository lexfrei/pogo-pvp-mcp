package tools

// Species ids and evolution-item ids shared between the curated
// evolution-item table (evolution_items.go) and its in-package test
// (evolution_items_test.go). Both files live in `package tools`, so
// these recurring pvpoke ids are declared once here.

// Species ids that appear in both the requirement table and its test.
const (
	speciesVileplume  = "vileplume"
	speciesBellossom  = "bellossom"
	speciesSlowbro    = "slowbro"
	speciesSlowking   = "slowking"
	speciesPoliwrath  = "poliwrath"
	speciesPolitoed   = "politoed"
	speciesHuntail    = "huntail"
	speciesGorebyss   = "gorebyss"
	speciesVaporeon   = "vaporeon"
	speciesJolteon    = "jolteon"
	speciesFlareon    = "flareon"
	speciesEspeon     = "espeon"
	speciesUmbreon    = "umbreon"
	speciesLeafeon    = "leafeon"
	speciesGlaceon    = "glaceon"
	speciesSylveon    = "sylveon"
	speciesHitmonlee  = "hitmonlee"
	speciesHitmonchan = "hitmonchan"
	speciesHitmontop  = "hitmontop"
	speciesSunflora   = "sunflora"
	speciesKingdra    = "kingdra"
	speciesScizor     = "scizor"
	speciesSteelix    = "steelix"
	speciesPorygon2   = "porygon2"
	speciesPorygonZ   = "porygon_z"
	speciesRhyperior  = "rhyperior"
	speciesElectivire = "electivire"
	speciesMagmortar  = "magmortar"
	speciesGliscor    = "gliscor"
	speciesDusknoir   = "dusknoir"
	speciesTogekiss   = "togekiss"
	speciesMagnezone  = "magnezone"
	speciesProbopass  = "probopass"
)

// Evolution-item ids that appear two or more times across the table
// and its test.
const (
	itemSunStone     = "sun_stone"
	itemKingRock     = "king_rock"
	itemDragonScale  = "dragon_scale"
	itemMetalCoat    = "metal_coat"
	itemUpGrade      = "up_grade"
	itemSinnohStone  = "sinnoh_stone"
	itemMossyLure    = "mossy_lure"
	itemGlacialLure  = "glacial_lure"
	itemMagneticLure = "magnetic_lure"
)

// Shared free-text Notes strings used by two or more table entries.
const (
	// notesMagneticLure is the evolve-near-a-lure note for the two
	// Magnetic-Lure-gated species (magnezone, probopass).
	notesMagneticLure = "evolve near a Magnetic Lure module"
	// notesClamperlRandom is the shared random-pick note for the
	// clamperl split (huntail / gorebyss).
	notesClamperlRandom = "random pick (no item in Pokémon GO, unlike mainline)"
)
