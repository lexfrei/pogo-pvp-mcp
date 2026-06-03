package tools

// The 18 canonical pvpoke PvP type names. These are the lowercase keys
// pvpoke uses across its gamemaster, rankings, and type chart. They are
// shared by the known-type validation table (type_matchup.go) and the
// weather-boost table (weather_boost.go) so the literal type names live
// in exactly one place.
const (
	typeNormal   = "normal"
	typeFire     = "fire"
	typeWater    = "water"
	typeElectric = "electric"
	typeGrass    = "grass"
	typeIce      = "ice"
	typeFighting = "fighting"
	typePoison   = "poison"
	typeGround   = "ground"
	typeFlying   = "flying"
	typePsychic  = "psychic"
	typeBug      = "bug"
	typeRock     = "rock"
	typeGhost    = "ghost"
	typeDragon   = "dragon"
	typeDark     = "dark"
	typeSteel    = "steel"
	typeFairy    = "fairy"
)
