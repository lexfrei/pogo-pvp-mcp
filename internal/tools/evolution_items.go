package tools

// evolution_items.go carries a hardcoded branching-evolution
// requirement table. pvpoke's gamemaster.json does NOT publish
// evolution items — Species.Evolutions is a plain []string of
// child ids, with no item or candy metadata. Clients asking
// "should I evolve gloom to vileplume or bellossom, and what
// does each cost?" need that data to make the recommendation.
//
// Source: Bulbapedia (authoritative for Niantic's GO mechanics)
// snapshot 2026-04. Cross-referenced against PokéMiners
// game-master dumps where available. Niantic changes these
// requirements very rarely — the last adjustment was the 2024
// eeveelution buddy-distance tweak. If the table drifts from
// in-game reality, users should report via pvp_report_data_issue.
//
// Scope: branching evolutions where an item OR a non-trivial
// mechanic selects the branch (gloom → vileplume OR bellossom).
// Linear evolutions without item requirements (bulbasaur →
// ivysaur → venusaur) are intentionally NOT in the table — they
// don't need requirement disclosure because the enumerator
// doesn't produce alternatives for them.
//
// When pvpoke / engine eventually publish evolution-item data
// natively, this table can be deleted.

// Canonical per-step evolution candy tiers in Pokémon GO. The
// three values cover every entry in the branching-evolution table.
const (
	evolveCandy25  = 25
	evolveCandy50  = 50
	evolveCandy100 = 100
)

//nolint:gochecknoglobals // hardcoded canonical table; Niantic changes these ~never
var evolutionItemRequirements = map[string]EvolutionItemRequirement{
	// Stone-gated branches.
	"vileplume": {Candy: evolveCandy100},
	"bellossom": {Item: "sun_stone", Candy: evolveCandy100},
	"sunflora":  {Item: "sun_stone", Candy: evolveCandy25},

	// King's Rock branches.
	"slowbro":   {Candy: evolveCandy50},
	"slowking":  {Item: "king_rock", Candy: evolveCandy50},
	"poliwrath": {Candy: evolveCandy100},
	"politoed":  {Item: "king_rock", Candy: evolveCandy100},

	// Dragon Scale / Metal Coat / Up-grade family.
	"kingdra":  {Item: "dragon_scale", Candy: evolveCandy100},
	"scizor":   {Item: "metal_coat", Candy: evolveCandy50},
	"steelix":  {Item: "metal_coat", Candy: evolveCandy50},
	"porygon2": {Item: "up_grade", Candy: evolveCandy25},

	// Clamperl split.
	"huntail":  {Item: "deep_sea_tooth", Candy: evolveCandy50},
	"gorebyss": {Item: "deep_sea_scale", Candy: evolveCandy50},

	// Sinnoh / Unova stones.
	"magnezone":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"rhyperior":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"electivire": {Item: "sinnoh_stone", Candy: evolveCandy100},
	"magmortar":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"porygon_z":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"gliscor":    {Item: "sinnoh_stone", Candy: evolveCandy100},
	"dusknoir":   {Item: "sinnoh_stone", Candy: evolveCandy100},
	"togekiss":   {Item: "sinnoh_stone", Candy: evolveCandy100},

	// Lure-gated evolutions.
	"probopass": {Item: "mossy_lure", Candy: evolveCandy50, Notes: "evolve near a Mossy Lure module"},
	"leafeon":   {Item: "mossy_lure", Candy: evolveCandy25, Notes: "evolve near a Mossy Lure module"},
	"glaceon":   {Item: "glacial_lure", Candy: evolveCandy25, Notes: "evolve near a Glacial Lure module"},

	// Eevee branches that do not need an item.
	"vaporeon": {Candy: evolveCandy25, Notes: "random pick unless name-trick used (Rainer)"},
	"jolteon":  {Candy: evolveCandy25, Notes: "random pick unless name-trick used (Sparky)"},
	"flareon":  {Candy: evolveCandy25, Notes: "random pick unless name-trick used (Pyro)"},
	"espeon":   {Notes: "walk 10 km as buddy + evolve during the day (one per name-trick Sakura)"},
	"umbreon":  {Notes: "walk 10 km as buddy + evolve during the night (one per name-trick Tamao)"},
	"sylveon":  {Candy: evolveCandy25, Notes: "earn 70 buddy hearts (one per name-trick Kira)"},

	// Tyrogue split (stat-based, no item).
	"hitmonlee":  {Candy: evolveCandy25, Notes: "highest ATK IV selects hitmonlee"},
	"hitmonchan": {Candy: evolveCandy25, Notes: "highest DEF IV selects hitmonchan"},
	"hitmontop":  {Candy: evolveCandy25, Notes: "highest HP IV selects hitmontop"},
}

// evolutionRequirementFor returns the curated requirement for
// speciesID if the species is in the branching-evolution table.
// Returns (nil, false) for species outside the table (linear
// evolutions, terminal species, anything Niantic gates behind a
// mechanic not in the table yet). Callers should fall back to
// their own source for per-step candy / item needs when ok=false.
func evolutionRequirementFor(speciesID string) (*EvolutionItemRequirement, bool) {
	req, ok := evolutionItemRequirements[speciesID]
	if !ok {
		return nil, false
	}

	out := req

	return &out, true
}
