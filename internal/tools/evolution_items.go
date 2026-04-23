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
	// Stone-gated branches (Gloom, Sunkern).
	"vileplume": {Candy: evolveCandy100},
	"bellossom": {Item: "sun_stone", Candy: evolveCandy100},
	"sunflora":  {Item: "sun_stone", Candy: evolveCandy25},

	// King's Rock branches (Slowpoke, Poliwhirl).
	"slowbro":   {Candy: evolveCandy50},
	"slowking":  {Item: "king_rock", Candy: evolveCandy50},
	"poliwrath": {Candy: evolveCandy100},
	"politoed":  {Item: "king_rock", Candy: evolveCandy100},

	// Trade-evolution proxies (Dragon Scale, Metal Coat, Up-grade).
	"kingdra":  {Item: "dragon_scale", Candy: evolveCandy100},
	"scizor":   {Item: "metal_coat", Candy: evolveCandy50},
	"steelix":  {Item: "metal_coat", Candy: evolveCandy50},
	"porygon2": {Item: "up_grade", Candy: evolveCandy25},

	// Clamperl split — pure RNG in Pokémon GO (no item, unlike
	// mainline games' Deep Sea Tooth / Deep Sea Scale).
	"huntail":  {Candy: evolveCandy50, Notes: "random pick (no item in Pokémon GO, unlike mainline)"},
	"gorebyss": {Candy: evolveCandy50, Notes: "random pick (no item in Pokémon GO, unlike mainline)"},

	// Sinnoh-stone evolutions.
	"rhyperior":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"electivire": {Item: "sinnoh_stone", Candy: evolveCandy100},
	"magmortar":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"porygon_z":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"gliscor":    {Item: "sinnoh_stone", Candy: evolveCandy100},
	"dusknoir":   {Item: "sinnoh_stone", Candy: evolveCandy100},
	"togekiss":   {Item: "sinnoh_stone", Candy: evolveCandy100},

	// Lure-gated evolutions.
	"magnezone": {Item: "magnetic_lure", Candy: evolveCandy100, Notes: "evolve near a Magnetic Lure module"},
	"probopass": {Item: "magnetic_lure", Candy: evolveCandy50, Notes: "evolve near a Magnetic Lure module"},
	"leafeon":   {Item: "mossy_lure", Candy: evolveCandy25, Notes: "evolve near a Mossy Lure module"},
	"glaceon":   {Item: "glacial_lure", Candy: evolveCandy25, Notes: "evolve near a Glacial Lure module"},

	// Eevee branches — random pick unless name-trick used.
	"vaporeon": {Candy: evolveCandy25, Notes: "random pick unless name-trick used (Rainer)"},
	"jolteon":  {Candy: evolveCandy25, Notes: "random pick unless name-trick used (Sparky)"},
	"flareon":  {Candy: evolveCandy25, Notes: "random pick unless name-trick used (Pyro)"},
	// Espeon / Umbreon: 25 candy plus the 10 km-buddy + time-of-day
	// gate. The name-trick bypass works once per account.
	"espeon":  {Candy: evolveCandy25, Notes: "walk 10 km as buddy + evolve during the day (one per name-trick Sakura)"},
	"umbreon": {Candy: evolveCandy25, Notes: "walk 10 km as buddy + evolve during the night (one per name-trick Tamao)"},
	"sylveon": {Candy: evolveCandy25, Notes: "earn 70 buddy hearts (one per name-trick Kira)"},

	// Tyrogue split (stat-based, no item).
	"hitmonlee":  {Candy: evolveCandy25, Notes: "highest ATK IV selects hitmonlee"},
	"hitmonchan": {Candy: evolveCandy25, Notes: "highest DEF IV selects hitmonchan"},
	"hitmontop":  {Candy: evolveCandy25, Notes: "highest HP IV selects hitmontop"},
}

// evolutionRequirementFor returns the curated requirement for
// speciesID when the species is in the branching-evolution table,
// or nil when it is not (linear evolutions, terminal species,
// mechanics we do not yet model). Callers should treat nil as
// "consult your own data source" rather than "no requirement".
// Hands back an independent struct copy so caller mutation cannot
// pollute the shared table.
func evolutionRequirementFor(speciesID string) *EvolutionItemRequirement {
	req, ok := evolutionItemRequirements[speciesID]
	if !ok {
		return nil
	}

	out := req

	return &out
}
