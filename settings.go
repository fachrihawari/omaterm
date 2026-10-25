package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings are the user's preferences, in settings.json beside state.json.
// Fields missing from the file keep their defaults, so adding one never
// needs a migration.
type Settings struct {
	// Theme is "omarchy" (follow the active Omarchy theme), "dark" or
	// "light".
	Theme string `json:"theme"`
	// Density is "comfortable" or "compact".
	Density string `json:"density"`
	// Background is "wash" (a soft gradient behind the cards) or "plain".
	Background   string `json:"background"`
	DimUnfocused bool   `json:"dimUnfocused"`
	// FocusFollowsMouse gives the keyboard to the pane the pointer enters.
	FocusFollowsMouse bool `json:"focusFollowsMouse"`
	// MinimumContrast is the terminals' minimum contrast ratio; 0 picks 1
	// in dark themes and 3 in light ones.
	MinimumContrast float32 `json:"minimumContrast"`
	ReduceMotion    bool    `json:"reduceMotion"`

	Notify NotifySettings `json:"notify"`

	// ControlSocket lets the omaterm command reach the app; RemoteControl
	// ("Let Agents Control the App") lets it run the app's commands and
	// drive browser panes, not only sessions.
	ControlSocket bool `json:"controlSocket"`
	RemoteControl bool `json:"remoteControl"`

	// Daemon runs the sessions' shells in a process of their own, so
	// they go on when the window closes.
	Daemon          bool `json:"daemon"`
	RestoreSessions bool `json:"restoreSessions"`
	ResumeAgents    bool `json:"resumeAgents"`
	GitStatus       bool `json:"gitStatus"`
	BrowserBlockAds bool `json:"browserBlockAds"`
}

// NotifySettings choose which session changes notify while you look away.
type NotifySettings struct {
	Waiting bool `json:"waiting"`
	Done    bool `json:"done"`
	Error   bool `json:"error"`
}

func defaultSettings() Settings {
	return Settings{
		Theme:             themeOmarchy,
		Density:           "comfortable",
		Background:        "wash",
		DimUnfocused:      true,
		FocusFollowsMouse: true,
		Notify:            NotifySettings{Waiting: true, Done: true, Error: true},
		ControlSocket:     true,
		Daemon:            true,
		RestoreSessions:   true,
		ResumeAgents:      true,
		GitStatus:         true,
	}
}

func settingsFilePath() string {
	return filepath.Join(filepath.Dir(stateFilePath()), "settings.json")
}

// loadSettings reads settings.json over the defaults; a missing or broken
// file gives the defaults.
func loadSettings() Settings {
	s := defaultSettings()
	if data, err := os.ReadFile(settingsFilePath()); err == nil {
		json.Unmarshal(data, &s)
	}
	switch s.Theme {
	case themeOmarchy, themeDark, themeLight:
	default:
		s.Theme = themeOmarchy
	}
	return s
}

// saveSettings writes settings.json atomically. Tests, which have no
// window, never write.
func (a *appState) saveSettings() {
	if a.win == nil {
		return
	}
	a.settings.Theme = a.themeMode
	data, err := json.MarshalIndent(a.settings, "", "  ")
	if err != nil {
		return
	}
	path := settingsFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	os.Rename(tmp, path)
}
