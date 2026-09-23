package fav

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds the settings panel values (~/.agent/fav/config.json); FAV_ICONS and --no-mouse override it.
type Config struct {
	RelativeTime bool   `json:"relative_time"` // today 16:53 / yesterday 09:12 / Wed / 09-12 / 2025-12-01
	DefaultView  string `json:"default_view"`  // favorites | sessions | projects
	Sort         string `json:"sort"`          // active | started | favorited | turns
	MinTurns     int    `json:"min_turns"`
	WheelStep    int    `json:"wheel_step"`
	WheelSpeed   string `json:"wheel_speed"` // fling acceleration: off | normal | fast
	Icons        string `json:"icons"`       // ascii (default) | nerd
	Mouse        bool   `json:"mouse"`
	LiveSort     string `json:"live_sort,omitempty"`    // Agents page order: started (default) | group | active
	ProjectSort  string `json:"project_sort,omitempty"` // projects page group order: active (default) | count | name
	IDE          string `json:"ide,omitempty"`          // app name or path for "open in IDE"; empty = Rebased
	Lang         string `json:"lang,omitempty"`         // "" follows the system | zh | en
	TrashDays    int    `json:"trash_days"`             // days kept in trash; 0 = never auto-purge
	ToolOutput   int    `json:"tool_output_lines"`      // lines of each tool output kept for message search; 0 = none
}

func DefaultConfig() Config {
	return Config{RelativeTime: true, DefaultView: "favorites", Sort: "active", MinTurns: 3, WheelStep: 3, WheelSpeed: "normal", Icons: "ascii", Mouse: true, TrashDays: 30, ToolOutput: 3}
}

func ConfigPath() string { return filepath.Join(Home(), "config.json") }

func LoadConfig() Config {
	c := DefaultConfig()
	b, err := os.ReadFile(ConfigPath())
	if err != nil {
		return c
	}
	json.Unmarshal(b, &c)
	if c.MinTurns < 1 {
		c.MinTurns = 1
	}
	if c.WheelSpeed == "" {
		c.WheelSpeed = "normal"
	}
	if c.WheelStep < 1 {
		c.WheelStep = 1
	}
	return c
}

func (c Config) Save() error {
	if err := os.MkdirAll(Home(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), append(b, '\n'), 0o644)
}
