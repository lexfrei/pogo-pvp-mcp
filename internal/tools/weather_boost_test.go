package tools_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/lexfrei/pogo-pvp-mcp/internal/tools"
)

// TestWeatherBoost_FullTable verifies that an empty Weather param
// returns all seven weather rows with the expected shape.
func TestWeatherBoost_FullTable(t *testing.T) {
	t.Parallel()

	tool := tools.NewWeatherBoostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.WeatherBoostParams{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if len(result.Entries) != 7 {
		t.Fatalf("Entries len = %d, want 7 (canonical weather count)", len(result.Entries))
	}

	if result.BoostMultiplier != 1.1 {
		t.Errorf("BoostMultiplier = %f, want 1.1", result.BoostMultiplier)
	}

	// Every entry must have a non-empty BoostedTypes slice.
	for _, entry := range result.Entries {
		if len(entry.BoostedTypes) == 0 {
			t.Errorf("entry %q has empty BoostedTypes", entry.Weather)
		}
	}
}

// TestWeatherBoost_SingleWeather verifies the single-query path.
func TestWeatherBoost_SingleWeather(t *testing.T) {
	t.Parallel()

	const weatherSunny = "sunny"

	tool := tools.NewWeatherBoostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.WeatherBoostParams{Weather: weatherSunny})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if result.Query != weatherSunny {
		t.Errorf("Query = %q, want %q", result.Query, weatherSunny)
	}

	if len(result.Entries) != 1 {
		t.Fatalf("Entries len = %d, want 1", len(result.Entries))
	}

	sunny := result.Entries[0]
	if sunny.Weather != weatherSunny {
		t.Errorf("Weather = %q, want %q", sunny.Weather, weatherSunny)
	}

	if !slices.Contains(sunny.BoostedTypes, "fire") {
		t.Errorf("BoostedTypes = %v, want to contain \"fire\"", sunny.BoostedTypes)
	}
	if !slices.Contains(sunny.BoostedTypes, "grass") {
		t.Errorf("BoostedTypes = %v, want to contain \"grass\"", sunny.BoostedTypes)
	}
	if !slices.Contains(sunny.BoostedTypes, "ground") {
		t.Errorf("BoostedTypes = %v, want to contain \"ground\"", sunny.BoostedTypes)
	}
}

// TestWeatherBoost_UnknownWeather rejects invalid weather names.
func TestWeatherBoost_UnknownWeather(t *testing.T) {
	t.Parallel()

	tool := tools.NewWeatherBoostTool()
	handler := tool.Handler()

	_, _, err := handler(t.Context(), nil, tools.WeatherBoostParams{Weather: "blizzard"})
	if !errors.Is(err, tools.ErrUnknownWeather) {
		t.Errorf("error = %v, want wrapping ErrUnknownWeather", err)
	}
}

// TestWeatherBoost_AllTypesCovered pins the invariant that every
// one of the 18 canonical pvpoke types appears in exactly one
// weather's BoostedTypes — the Niantic weather system is designed
// to cover all types without overlap.
func TestWeatherBoost_AllTypesCovered(t *testing.T) {
	t.Parallel()

	tool := tools.NewWeatherBoostTool()
	handler := tool.Handler()

	_, result, err := handler(t.Context(), nil, tools.WeatherBoostParams{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	counts := make(map[string]int)
	for _, entry := range result.Entries {
		for _, typeName := range entry.BoostedTypes {
			counts[typeName]++
		}
	}

	canonicalTypes := []string{
		"normal", "fighting", "flying", "poison", "ground", "rock",
		"bug", "ghost", "steel", "fire", "water", "grass",
		"electric", "psychic", "ice", "dragon", "dark", "fairy",
	}

	for _, typeName := range canonicalTypes {
		if counts[typeName] != 1 {
			t.Errorf("type %q appears %d times across weathers, want exactly 1",
				typeName, counts[typeName])
		}
	}

	if len(counts) != len(canonicalTypes) {
		t.Errorf("distinct boosted types = %d, want %d (canonical pvpoke type count)",
			len(counts), len(canonicalTypes))
	}
}

// TestWeatherBoost_ResultIsDefensivelyCloned pins that the returned
// BoostedTypes slice is a clone, not an alias into the package
// weatherBoostTable — mutating the response must not corrupt the
// shared table for subsequent calls.
func TestWeatherBoost_ResultIsDefensivelyCloned(t *testing.T) {
	t.Parallel()

	tool := tools.NewWeatherBoostTool()
	handler := tool.Handler()

	_, first, err := handler(t.Context(), nil, tools.WeatherBoostParams{Weather: "rainy"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	first.Entries[0].BoostedTypes[0] = "MUTATED"

	_, second, err := handler(t.Context(), nil, tools.WeatherBoostParams{Weather: "rainy"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if slices.Contains(second.Entries[0].BoostedTypes, "MUTATED") {
		t.Errorf("second call surfaced mutated data — slice is aliased, not cloned")
	}
}
