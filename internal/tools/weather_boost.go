package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrUnknownWeather is returned when the weather name is not one of
// the seven canonical Pokémon GO conditions.
var ErrUnknownWeather = errors.New("unknown weather condition")

// WeatherBoostParams is the JSON input for pvp_weather_boost. Empty
// Weather returns the full per-weather table; a named weather
// returns only the types boosted by that weather. Matching is
// case-insensitive to match pvp_type_matchup's input handling.
type WeatherBoostParams struct {
	Weather string `json:"weather,omitempty" jsonschema:"weather name (sunny/rainy/partly_cloudy/cloudy/windy/snow/fog); empty = full table"`
}

// WeatherBoostEntry is one (weather → boosted types) row.
type WeatherBoostEntry struct {
	Weather      string   `json:"weather"`
	Display      string   `json:"display"`
	BoostedTypes []string `json:"boosted_types"`
}

// WeatherBoostResult is the JSON output. When Weather is specified
// in the params, Entries has exactly one row; otherwise the full
// seven-row table is returned.
//
// IMPORTANT: PvEBoostMultiplier (1.2) is the PvE / wild-spawn / raid
// bonus. Niantic's Trainer Battles (PvP / GBL / remote battles) do
// NOT apply weather boost at all — attackers and defenders can be in
// different real-world weather conditions, so the mechanic is
// excluded from PvP. This tool exists as a reference lookup; the
// battle simulator engine (pogo-pvp-engine) never consumes the
// multiplier. Do not apply it to a PvP rating calculation.
type WeatherBoostResult struct {
	Query              string              `json:"query,omitempty"`
	Entries            []WeatherBoostEntry `json:"entries"`
	PvEBoostMultiplier float64             `json:"pve_boost_multiplier"`
	AppliesToPvP       bool                `json:"applies_to_pvp"`
	PvPNote            string              `json:"pvp_note"`
}

// WeatherBoostTool is a read-only lookup with no external dependencies.
type WeatherBoostTool struct{}

// NewWeatherBoostTool constructs the tool. No manager dependencies —
// the weather boost table is a fixed Pokémon GO constant.
func NewWeatherBoostTool() *WeatherBoostTool {
	return &WeatherBoostTool{}
}

// pveWeatherBoostMultiplier is the canonical Pokémon GO damage
// bonus applied to moves of a boosted type during matching weather
// in PvE contexts (wild spawns, raids, gyms, Go Rocket battles).
// Niantic has documented this as a flat 20% bonus since launch.
// Trainer Battles (GO Battle League, remote / friend PvP) never
// apply this multiplier — the mechanic is excluded from PvP.
const pveWeatherBoostMultiplier = 1.2

// pvpWeatherNote is the prominent disclaimer surfaced on every
// response so an LLM caller cannot accidentally pipe the multiplier
// into a PvP damage computation.
const pvpWeatherNote = "Weather boost is NOT applied in Trainer Battles (PvP / GO Battle League). " +
	"The pve_boost_multiplier is a reference value for wild spawns / raids only."

// weatherSpec pairs a canonical weather name with its display label
// and the list of types it boosts.
type weatherSpec struct {
	Weather      string
	Display      string
	BoostedTypes []string
}

// weatherBoostTable is the canonical mapping of weather → boosted
// types from the Niantic weather system. Kept in ascending weather
// name order so iteration is deterministic and knownWeatherNames
// does not need to re-sort.
//
//nolint:gochecknoglobals // fixed domain lookup table
var weatherBoostTable = []weatherSpec{
	{Weather: "cloudy", Display: "Cloudy", BoostedTypes: []string{"fairy", "fighting", "poison"}},
	{Weather: "fog", Display: "Fog", BoostedTypes: []string{"dark", "ghost"}},
	{Weather: "partly_cloudy", Display: "Partly Cloudy", BoostedTypes: []string{"normal", "rock"}},
	{Weather: "rainy", Display: "Rainy", BoostedTypes: []string{"bug", "electric", "water"}},
	{Weather: "snow", Display: "Snow", BoostedTypes: []string{"ice", "steel"}},
	{Weather: "sunny", Display: "Sunny / Clear", BoostedTypes: []string{"fire", "grass", "ground"}},
	{Weather: "windy", Display: "Windy", BoostedTypes: []string{"dragon", "flying", "psychic"}},
}

const weatherBoostToolDescription = "Look up Niantic's weather → boosted-types table (sunny/rainy/partly_cloudy/" +
	"cloudy/windy/snow/fog) for REFERENCE ONLY. The 1.2× damage bonus applies to wild spawns and raids; it is " +
	"NEVER applied in PvP / GO Battle League / remote Trainer Battles, so do not consume the multiplier in a " +
	"PvP damage calculation. Pass weather=\"\" to get the full seven-row table."

// Tool returns the MCP tool registration.
func (*WeatherBoostTool) Tool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "pvp_weather_boost",
		Description: weatherBoostToolDescription,
	}
}

// Handler returns the MCP-typed handler function.
func (tool *WeatherBoostTool) Handler() mcp.ToolHandlerFor[WeatherBoostParams, WeatherBoostResult] {
	return tool.handle
}

// handle performs the table lookup. No gamemaster / rankings /
// network state is touched — the table is compile-time constant.
func (*WeatherBoostTool) handle(
	_ context.Context,
	_ *mcp.CallToolRequest,
	params WeatherBoostParams,
) (*mcp.CallToolResult, WeatherBoostResult, error) {
	if params.Weather == "" {
		return nil, buildFullWeatherTable(), nil
	}

	entry, ok := lookupWeather(strings.ToLower(params.Weather))
	if !ok {
		return nil, WeatherBoostResult{}, fmt.Errorf("%w: %q (allowed: %s)",
			ErrUnknownWeather, params.Weather, knownWeatherNames())
	}

	return nil, WeatherBoostResult{
		Query:              params.Weather,
		Entries:            []WeatherBoostEntry{entry},
		PvEBoostMultiplier: pveWeatherBoostMultiplier,
		AppliesToPvP:       false,
		PvPNote:            pvpWeatherNote,
	}, nil
}

// lookupWeather returns the table row for a canonical weather name,
// or ok=false if the name isn't one of the seven conditions.
func lookupWeather(name string) (WeatherBoostEntry, bool) {
	for _, spec := range weatherBoostTable {
		if spec.Weather != name {
			continue
		}

		return WeatherBoostEntry{
			Weather:      spec.Weather,
			Display:      spec.Display,
			BoostedTypes: slices.Clone(spec.BoostedTypes),
		}, true
	}

	return WeatherBoostEntry{}, false
}

// buildFullWeatherTable returns every row in weather-name order.
// Used when the caller passes an empty Weather param.
func buildFullWeatherTable() WeatherBoostResult {
	entries := make([]WeatherBoostEntry, 0, len(weatherBoostTable))

	for _, spec := range weatherBoostTable {
		entries = append(entries, WeatherBoostEntry{
			Weather:      spec.Weather,
			Display:      spec.Display,
			BoostedTypes: slices.Clone(spec.BoostedTypes),
		})
	}

	return WeatherBoostResult{
		Entries:            entries,
		PvEBoostMultiplier: pveWeatherBoostMultiplier,
		AppliesToPvP:       false,
		PvPNote:            pvpWeatherNote,
	}
}

// knownWeatherNames returns the canonical weather names in table
// order (already sorted by spec at declaration time).
func knownWeatherNames() []string {
	out := make([]string, 0, len(weatherBoostTable))
	for _, spec := range weatherBoostTable {
		out = append(out, spec.Weather)
	}

	return out
}
