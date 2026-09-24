package fav

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/oxsean/fav/internal/fileio"
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
	Notify       string `json:"notify,omitempty"`       // a running session starts to need you: off (footer only, default) | bell
	ProjectSort  string `json:"project_sort,omitempty"` // projects page group order: active (default) | count | name
	IDE          string `json:"ide,omitempty"`          // app name or path for "open in IDE"; empty = Rebased
	Lang         string `json:"lang,omitempty"`         // "" follows the system | zh | en
	TrashDays    int    `json:"trash_days"`             // days kept in trash; 0 = never auto-purge
	ToolOutput   int    `json:"tool_output_lines"`      // lines of each tool output kept for message search; 0 = none
	ResumeIn     string `json:"resume_in,omitempty"`    // what Enter on resume opens: terminal (default) | app | origin (the app for sessions started there)
	Hosts        []Host `json:"hosts,omitempty"`

	Agents      []AgentProfile           `json:"agents,omitempty"`
	Machines    map[string]MachineConfig `json:"machines,omitempty"`
	Node        NodeConfig               `json:"node,omitempty"`
	Coordinator *CoordinatorConfig       `json:"coordinator,omitempty"` // mode 2: the server this machine's clients use
}

// AgentProfile is one way to run an agent.
type AgentProfile struct {
	Name       string   `json:"name"`
	Provider   string   `json:"provider"`
	Model      string   `json:"model,omitempty"`
	Permission string   `json:"permission,omitempty"` // claude: --permission-mode; codex: --sandbox
	Args       []string `json:"args,omitempty"`       // added to every launch
	Command    []string `json:"command,omitempty"`    // provider "command": the argv template
	Stdin      bool     `json:"stdin,omitempty"`      // provider "command": the task brief goes to stdin
	Machine    string   `json:"machine,omitempty"`    // the only machine it runs on
}

// MachineConfig tunes one machine the coordinator runs agents on ("local" is this one).
type MachineConfig struct {
	Slots int `json:"slots,omitempty"` // runs at once; default 2
}

// NodeConfig is what this machine lets a coordinator do; mode 2 requires AllowDirs.
type NodeConfig struct {
	AllowDirs     []string `json:"allow_dirs,omitempty"`
	AllowBypass   bool     `json:"allow_bypass,omitempty"`
	AllowProfiles []string `json:"allow_profiles,omitempty"`
}

type CoordinatorConfig struct {
	URL       string `json:"url"`
	TokenFile string `json:"token_file,omitempty"`
}

// Host is another machine whose sessions fav shows, reached with `ssh <SSH> <Fav…> rpc`.
type Host struct {
	Name  string   `json:"name"`            // shown on cards and in host:<name>
	SSH   string   `json:"ssh,omitempty"`   // an alias from ~/.ssh/config; empty runs Fav here (another config dir, tests)
	Fav   []string `json:"fav,omitempty"`   // the remote command that runs fav, one argument per element; default ["fav"]
	Shell string   `json:"shell,omitempty"` // how the remote login shell quotes: posix | cmd | powershell; guessed from Fav when empty
}

// ResumeIn values.
const (
	NotifyOff  = "off"
	NotifyBell = "bell"

	ResumeTerminal = "terminal"
	ResumeApp      = "app"
	ResumeOrigin   = "origin"
)

func DefaultConfig() Config {
	return Config{RelativeTime: true, DefaultView: "favorites", Sort: "active", MinTurns: 3, WheelStep: 3, WheelSpeed: "normal", Icons: "ascii", Mouse: true, TrashDays: 30, ToolOutput: 3, ResumeIn: ResumeTerminal}
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
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fileio.WriteFile(ConfigPath(), append(b, '\n'), 0o644)
}
