package tools

// evolution_items.go carries a hardcoded branching-evolution
// requirement table. pvpoke's gamemaster.json does NOT publish
// evolution items — Species.Evolutions is a plain []string of
// child ids, with no item or candy metadata. Clients asking
// "should I evolve gloom to vileplume or bellossom, and what
// does each cost?" need that data to make the recommendation.
//
// Source: Bulbapedia cross-referenced against pokemongohub.net +
// gameinfo.io, snapshot 2026-04. Niantic changes these
// requirements rarely; drift should be reported via
// pvp_report_data_issue.
//
// Scope: ONLY branching chains where pvpoke's gamemaster lists
// more than one direct child of the base species AND the child
// is shipped in Pokémon GO. That's the only path
// enumerateBranchAlternatives (team_builder_evolve.go) calls
// evolutionRequirementFor — linear chains take the
// walkEvolutionChain branch instead and never hit this table.
// Ship-scope chains today (R6.7):
//   - gloom split  → vileplume (no item) / bellossom (Sun Stone)
//   - slowpoke     → slowbro (no item) / slowking (King's Rock)
//   - poliwhirl    → poliwrath (no item) / politoed (King's Rock)
//   - clamperl     → huntail / gorebyss (both random-pick in GO)
//   - eevee        → all eight eeveelutions
//   - tyrogue      → hitmonlee / hitmonchan / hitmontop
//
// Linear chains gated by an item in GO (scyther→scizor via Metal
// Coat, onix→steelix, porygon line via Up-Grade / Sinnoh Stone,
// rhydon→rhyperior, etc.) are deliberately OUT of scope here —
// surfacing their item cost requires wiring a Requirement field
// onto MemberCostBreakdown at the walkEvolutionChain path, which
// is a separate task. When pvpoke / engine eventually publish
// evolution-item data natively, this table can be deleted.

// Canonical per-step evolution candy tiers in Pokémon GO. The
// three values cover every entry in the branching-evolution table.
const (
	evolveCandy25  = 25
	evolveCandy50  = 50
	evolveCandy100 = 100
)

//nolint:gochecknoglobals // hardcoded canonical table; Niantic changes these ~never
var evolutionItemRequirements = map[string]EvolutionItemRequirement{
	// Gloom split (Sun Stone vs no-item).
	"vileplume": {Candy: evolveCandy100},
	"bellossom": {Item: "sun_stone", Candy: evolveCandy100},

	// Slowpoke split (King's Rock vs no-item).
	"slowbro":  {Candy: evolveCandy50},
	"slowking": {Item: "king_rock", Candy: evolveCandy50},

	// Poliwhirl split (King's Rock vs no-item).
	"poliwrath": {Candy: evolveCandy100},
	"politoed":  {Item: "king_rock", Candy: evolveCandy100},

	// Clamperl split — pure RNG in Pokémon GO (no item, unlike
	// mainline games' Deep Sea Tooth / Deep Sea Scale).
	"huntail":  {Candy: evolveCandy50, Notes: "random pick (no item in Pokémon GO, unlike mainline)"},
	"gorebyss": {Candy: evolveCandy50, Notes: "random pick (no item in Pokémon GO, unlike mainline)"},

	// Eevee branches — all eight descendants reach this table
	// because eevee is the canonical multi-branch chain in pvpoke.
	"vaporeon": {Candy: evolveCandy25, Notes: "random pick unless name-trick used (Rainer)"},
	"jolteon":  {Candy: evolveCandy25, Notes: "random pick unless name-trick used (Sparky)"},
	"flareon":  {Candy: evolveCandy25, Notes: "random pick unless name-trick used (Pyro)"},
	// Espeon / Umbreon: 25 candy plus the 10 km-buddy + time-of-day
	// gate. The name-trick bypass works once per account.
	"espeon":  {Candy: evolveCandy25, Notes: "walk 10 km as buddy + evolve during the day (one per name-trick Sakura)"},
	"umbreon": {Candy: evolveCandy25, Notes: "walk 10 km as buddy + evolve during the night (one per name-trick Tamao)"},
	"leafeon": {Item: "mossy_lure", Candy: evolveCandy25, Notes: "evolve near a Mossy Lure module (one per name-trick Linnea)"},
	"glaceon": {Item: "glacial_lure", Candy: evolveCandy25, Notes: "evolve near a Glacial Lure module (one per name-trick Rea)"},
	"sylveon": {Candy: evolveCandy25, Notes: "earn 70 buddy hearts (one per name-trick Kira)"},

	// Tyrogue split (stat-based, no item).
	"hitmonlee":  {Candy: evolveCandy25, Notes: "highest ATK IV selects hitmonlee"},
	"hitmonchan": {Candy: evolveCandy25, Notes: "highest DEF IV selects hitmonchan"},
	"hitmontop":  {Candy: evolveCandy25, Notes: "highest HP IV selects hitmontop"},

	// R7.P2 — linear item-gated steps. walkEvolutionChain picks up
	// the requirement while traversing a single-child chain (no
	// branching decision by the player, but the evolve-button still
	// demands an item on top of candy).
	"sunflora":   {Item: "sun_stone", Candy: evolveCandy50},
	"kingdra":    {Item: "dragon_scale", Candy: evolveCandy100},
	"scizor":     {Item: "metal_coat", Candy: evolveCandy50},
	"steelix":    {Item: "metal_coat", Candy: evolveCandy50},
	"porygon2":   {Item: "up_grade", Candy: evolveCandy25},
	"porygon_z":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"rhyperior":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"electivire": {Item: "sinnoh_stone", Candy: evolveCandy100},
	"magmortar":  {Item: "sinnoh_stone", Candy: evolveCandy100},
	"gliscor":    {Item: "sinnoh_stone", Candy: evolveCandy100},
	"dusknoir":   {Item: "sinnoh_stone", Candy: evolveCandy100},
	"togekiss":   {Item: "sinnoh_stone", Candy: evolveCandy100},
	"magnezone":  {Item: "magnetic_lure", Candy: evolveCandy100, Notes: "evolve near a Magnetic Lure module"},
	"probopass":  {Item: "magnetic_lure", Candy: evolveCandy50, Notes: "evolve near a Magnetic Lure module"},
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
