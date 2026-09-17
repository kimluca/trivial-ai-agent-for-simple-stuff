package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── calculator ───────────────────────────────────────────────────────────

type calculatorTool struct{}

func (calculatorTool) Name() string { return "calculator" }
func (calculatorTool) Description() string {
	return "Perform a single arithmetic operation: add, subtract, multiply, divide, power, or sqrt (sqrt only needs 'a')."
}
func (calculatorTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": []string{"add", "subtract", "multiply", "divide", "power", "sqrt"},
			},
			"a": map[string]any{"type": "number"},
			"b": map[string]any{"type": "number", "description": "not required for sqrt"},
		},
		"required": []string{"operation", "a"},
	}
}

func (calculatorTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		Operation string  `json:"operation"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid calculator input: %w", err)
	}
	var result float64
	switch args.Operation {
	case "add":
		result = args.A + args.B
	case "subtract":
		result = args.A - args.B
	case "multiply":
		result = args.A * args.B
	case "divide":
		if args.B == 0 {
			return "", fmt.Errorf("division by zero")
		}
		result = args.A / args.B
	case "power":
		result = math.Pow(args.A, args.B)
	case "sqrt":
		if args.A < 0 {
			return "", fmt.Errorf("cannot take sqrt of a negative number")
		}
		result = math.Sqrt(args.A)
	default:
		return "", fmt.Errorf("unknown operation %q", args.Operation)
	}
	return strconv.FormatFloat(result, 'f', -1, 64), nil
}

// ── weather (live, via Open-Meteo) ──────────────────────────────────────
// Real data, fetched from the internet at call time — no API key needed.
// Two requests: first geocode the city name to coordinates, then ask for
// the current conditions at that location. Open-Meteo is free and has no
// authentication, which keeps this project's "zero setup" promise intact
// while still giving genuinely accurate weather.

type weatherTool struct {
	client *http.Client
}

func newWeatherTool() *weatherTool {
	return &weatherTool{client: &http.Client{Timeout: 10 * time.Second}}
}

func (t *weatherTool) Name() string { return "get_weather" }
func (t *weatherTool) Description() string {
	return "Look up the current real-world weather for a city by name (live internet lookup, no API key needed)."
}
func (t *weatherTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{"type": "string"},
		},
		"required": []string{"city"},
	}
}

func (t *weatherTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid weather input: %w", err)
	}
	if strings.TrimSpace(args.City) == "" {
		return "", fmt.Errorf("city must not be empty")
	}

	lat, lon, resolved, err := t.geocode(args.City)
	if err != nil {
		return "", fmt.Errorf("couldn't locate %q: %w", args.City, err)
	}

	tempC, windKmh, code, err := t.currentConditions(lat, lon)
	if err != nil {
		return "", fmt.Errorf("couldn't fetch weather for %s: %w", resolved, err)
	}

	return fmt.Sprintf("%s: %.1f°C, %s, wind %.0f km/h (live, via Open-Meteo)",
		resolved, tempC, weatherCodeDescription(code), windKmh), nil
}

// geocode resolves a free-text city name to coordinates via Open-Meteo's
// geocoding endpoint and returns a human-readable "City, Country" name.
func (t *weatherTool) geocode(city string) (lat, lon float64, resolved string, err error) {
	u := "https://geocoding-api.open-meteo.com/v1/search?name=" + url.QueryEscape(city) + "&count=1&language=en&format=json"
	resp, err := t.client.Get(u)
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, "", fmt.Errorf("geocoding service returned status %d", resp.StatusCode)
	}

	var parsed struct {
		Results []struct {
			Name      string  `json:"name"`
			Country   string  `json:"country"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, 0, "", fmt.Errorf("decode geocoding response: %w", err)
	}
	if len(parsed.Results) == 0 {
		return 0, 0, "", fmt.Errorf("no such place found")
	}
	r := parsed.Results[0]
	name := r.Name
	if r.Country != "" {
		name += ", " + r.Country
	}
	return r.Latitude, r.Longitude, name, nil
}

// currentConditions fetches live temperature, wind speed, and a WMO
// weather code for the given coordinates.
func (t *weatherTool) currentConditions(lat, lon float64) (tempC, windKmh float64, code int, err error) {
	u := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%f&longitude=%f&current=temperature_2m,weather_code,wind_speed_10m",
		lat, lon,
	)
	resp, err := t.client.Get(u)
	if err != nil {
		return 0, 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, 0, fmt.Errorf("forecast service returned status %d", resp.StatusCode)
	}

	var parsed struct {
		Current struct {
			Temperature2m float64 `json:"temperature_2m"`
			WeatherCode   int     `json:"weather_code"`
			WindSpeed10m  float64 `json:"wind_speed_10m"`
		} `json:"current"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, 0, 0, fmt.Errorf("decode forecast response: %w", err)
	}
	return parsed.Current.Temperature2m, parsed.Current.WindSpeed10m, parsed.Current.WeatherCode, nil
}

// weatherCodeDescription translates a WMO weather code (the scheme
// Open-Meteo uses) into a short human-readable phrase.
func weatherCodeDescription(code int) string {
	switch {
	case code == 0:
		return "clear sky"
	case code >= 1 && code <= 3:
		return "partly cloudy"
	case code == 45 || code == 48:
		return "fog"
	case code >= 51 && code <= 57:
		return "drizzle"
	case code >= 61 && code <= 67:
		return "rain"
	case code >= 71 && code <= 77:
		return "snow"
	case code >= 80 && code <= 82:
		return "rain showers"
	case code >= 85 && code <= 86:
		return "snow showers"
	case code >= 95:
		return "thunderstorm"
	default:
		return "unknown conditions"
	}
}

// ── currency conversion (live, via Frankfurter/ECB) ─────────────────────
// Real exchange rates, fetched at call time from Frankfurter — a free,
// open-source API that mirrors European Central Bank reference rates and
// needs no API key. Rates update once per business day (~16:00 CET),
// which is the norm for reference-rate data like this.

type currencyTool struct {
	client *http.Client
}

func newCurrencyTool() *currencyTool {
	return &currencyTool{client: &http.Client{Timeout: 10 * time.Second}}
}

func (t *currencyTool) Name() string { return "convert_currency" }
func (t *currencyTool) Description() string {
	return "Convert an amount between currencies using live exchange rates (via the Frankfurter/ECB API, no API key needed). Use standard 3-letter ISO codes, e.g. USD, EUR, GBP, JPY."
}
func (t *currencyTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"amount": map[string]any{"type": "number"},
			"from":   map[string]any{"type": "string", "description": "3-letter ISO currency code, e.g. USD"},
			"to":     map[string]any{"type": "string", "description": "3-letter ISO currency code, e.g. EUR"},
		},
		"required": []string{"amount", "from", "to"},
	}
}

func (t *currencyTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		Amount float64 `json:"amount"`
		From   string  `json:"from"`
		To     string  `json:"to"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid currency input: %w", err)
	}
	from := strings.ToUpper(strings.TrimSpace(args.From))
	to := strings.ToUpper(strings.TrimSpace(args.To))
	if from == "" || to == "" {
		return "", fmt.Errorf("both 'from' and 'to' currency codes are required")
	}
	if from == to {
		return fmt.Sprintf("%.2f %s = %.2f %s", args.Amount, from, args.Amount, to), nil
	}

	u := fmt.Sprintf("https://api.frankfurter.dev/v1/latest?base=%s&symbols=%s", url.QueryEscape(from), url.QueryEscape(to))
	resp, err := t.client.Get(u)
	if err != nil {
		return "", fmt.Errorf("exchange rate lookup failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exchange rate service returned status %d (check the currency codes)", resp.StatusCode)
	}

	var parsed struct {
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode exchange rate response: %w", err)
	}
	rate, ok := parsed.Rates[to]
	if !ok {
		return "", fmt.Errorf("no rate found for %s → %s (check the currency codes)", from, to)
	}

	converted := args.Amount * rate
	return fmt.Sprintf("%.2f %s = %.2f %s (live rate as of %s: 1 %s = %.4f %s)",
		args.Amount, from, converted, to, parsed.Date, from, rate, to), nil
}

// ── world time ───────────────────────────────────────────────────────────

type timeTool struct{}

func (timeTool) Name() string { return "current_time" }
func (timeTool) Description() string {
	return "Get the current date and time in an IANA timezone, e.g. 'Europe/Berlin' or 'America/New_York'."
}
func (timeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"timezone": map[string]any{"type": "string"},
		},
		"required": []string{"timezone"},
	}
}

func (timeTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid time input: %w", err)
	}
	loc, err := time.LoadLocation(args.Timezone)
	if err != nil {
		return "", fmt.Errorf("unknown timezone %q: %w", args.Timezone, err)
	}
	return time.Now().In(loc).Format("Mon, 02 Jan 2006 15:04:05 MST"), nil
}

// ── notes / scratch memory (stateful) ───────────────────────────────────
// Demonstrates a tool that carries state across calls within one agent
// run — useful once the model starts doing genuinely multi-step work
// (jot something down in step 1, read it back in step 4).

type notesTool struct {
	mu    sync.Mutex
	store map[string]string
}

func newNotesTool() *notesTool {
	return &notesTool{store: make(map[string]string)}
}

func (t *notesTool) Name() string { return "notes" }
func (t *notesTool) Description() string {
	return "Save or recall short scratch notes by key. action='set' requires key+value, action='get' requires key, action='list' returns all keys."
}
func (t *notesTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"set", "get", "list"}},
			"key":    map[string]any{"type": "string"},
			"value":  map[string]any{"type": "string"},
		},
		"required": []string{"action"},
	}
}

func (t *notesTool) Execute(input json.RawMessage) (string, error) {
	var args struct {
		Action string `json:"action"`
		Key    string `json:"key"`
		Value  string `json:"value"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid notes input: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	switch args.Action {
	case "set":
		if args.Key == "" {
			return "", fmt.Errorf("key must not be empty")
		}
		t.store[args.Key] = args.Value
		return fmt.Sprintf("saved %q", args.Key), nil
	case "get":
		v, ok := t.store[args.Key]
		if !ok {
			return "", fmt.Errorf("no note under key %q", args.Key)
		}
		return v, nil
	case "list":
		keys := make([]string, 0, len(t.store))
		for k := range t.store {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return "(no notes yet)", nil
		}
		return strings.Join(keys, ", "), nil
	default:
		return "", fmt.Errorf("unknown action %q", args.Action)
	}
}
