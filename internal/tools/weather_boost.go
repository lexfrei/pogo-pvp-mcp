package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrUnknownWeather is returned when the weather name is not one of
// the seven canonical Pokémon GO conditions.
var ErrUnknownWeather = errors.New("unknown weather condition")

// WeatherBoostParams is the JSON input for pvp_weather_boost. Empty
// Weather returns the full per-weather table; a named weather
// returns only the types boosted by that weather.
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
// seven-row table is returned. BoostMultiplier is the flat PvP
// weather-bonus (1.1×) applied to moves of a boosted type on the
// turn damage is calculated.
type WeatherBoostResult struct {
	Query           string              `json:"query,omitempty"`
	Entries         []WeatherBoostEntry `json:"entries"`
	BoostMultiplier float64             `json:"boost_multiplier"`
}

// WeatherBoostTool is a read-only lookup with no external dependencies.
type WeatherBoostTool struct{}

// NewWeatherBoostTool constructs the tool. No manager dependencies —
// the weather boost table is a fixed Pokémon GO constant.
func NewWeatherBoostTool() *WeatherBoostTool {
	return &WeatherBoostTool{}
}

// weatherBoostMultiplier is the canonical Pokémon GO boost applied
// to moves of a boosted type while the current weather matches. It
// is identical in PvP and PvE and has not changed since the feature
// shipped.
const weatherBoostMultiplier = 1.1

// weatherSpec pairs a canonical weather name with its display label
// and the list of types it boosts.
type weatherSpec struct {
	Weather      string
	Display      string
	BoostedTypes []string
}

// weatherBoostTable is the canonical mapping of weather → boosted
// types from the Niantic weather system. Kept in ascending weather
// name order so iteration is deterministic.
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

const weatherBoostToolDescription = "Look up which move types are boosted under each Pokémon GO weather " +
	"condition (sunny/rainy/partly_cloudy/cloudy/windy/snow/fog) and the flat 1.1× multiplier applied to " +
	"matching-type damage. Pass weather=\"\" to get the full seven-row table."

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

	entry, ok := lookupWeather(params.Weather)
	if !ok {
		return nil, WeatherBoostResult{}, fmt.Errorf("%w: %q (allowed: %s)",
			ErrUnknownWeather, params.Weather, knownWeatherNames())
	}

	return nil, WeatherBoostResult{
		Query:           params.Weather,
		Entries:         []WeatherBoostEntry{entry},
		BoostMultiplier: weatherBoostMultiplier,
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
		Entries:         entries,
		BoostMultiplier: weatherBoostMultiplier,
	}
}

// knownWeatherNames returns the sorted list of canonical weather
// names, for error messages.
func knownWeatherNames() []string {
	out := make([]string, 0, len(weatherBoostTable))
	for _, spec := range weatherBoostTable {
		out = append(out, spec.Weather)
	}

	sort.Strings(out)

	return out
}
