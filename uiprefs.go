package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// uiPrefs is ui.json: how the window was laid out, which the app alone
// owns, apart from state.json, which a session daemon will own.
type uiPrefs struct {
	SidebarVisible   *bool   `json:"sidebarVisible,omitempty"`
	SidebarW         float32 `json:"sidebarW,omitempty"`
	InspectorVisible *bool   `json:"inspectorVisible,omitempty"`
	InspectorW       float32 `json:"inspectorW,omitempty"`
	InspectorTab     *int    `json:"inspectorTab,omitempty"`
	AgentSetupAsked  bool    `json:"agentSetupAsked,omitempty"`
	// SettingsPage is the Settings window's page, where it opens again.
	SettingsPage string `json:"settingsPage,omitempty"`
	// The worktree dialogs' last choices: the agent, copying .env files,
	// and squashing a merge.
	WorktreeAgent string `json:"worktreeAgent,omitempty"`
	WorktreeEnv   *bool  `json:"worktreeEnv,omitempty"`
	MergeSquash   *bool  `json:"mergeSquash,omitempty"`
	// DiffSplit and FontSize are read once, to move them to settings.json.
	DiffSplit *bool   `json:"diffSplit,omitempty"`
	FontSize  float32 `json:"fontSize,omitempty"`
}

func uiPrefsPath() string { return filepath.Join(filepath.Dir(stateFilePath()), "ui.json") }

// loadUIPrefs applies ui.json over the defaults.
func (a *appState) loadUIPrefs() {
	data, err := os.ReadFile(uiPrefsPath())
	if err != nil {
		return
	}
	var u uiPrefs
	if json.Unmarshal(data, &u) != nil {
		return
	}
	if u.SidebarVisible != nil {
		a.sidebarVisible = *u.SidebarVisible
	}
	if u.SidebarW >= 200 && u.SidebarW <= 360 {
		a.sidebarW = u.SidebarW
	}
	if u.InspectorVisible != nil {
		a.inspectorVisible = *u.InspectorVisible
	}
	if u.InspectorW >= 240 && u.InspectorW <= 600 {
		a.inspectorW = u.InspectorW
	}
	if u.InspectorTab != nil && *u.InspectorTab >= 0 && *u.InspectorTab <= 2 {
		a.inspectorTab = *u.InspectorTab
	} else if u.InspectorTab != nil && *u.InspectorTab == 3 {
		// The Changes tab, now the Git tab's Last turn.
		a.inspectorTab, a.changes.mode = 2, changesTurn
	}
	// The text size and the diff layout were kept here before they were
	// settings: settings.json takes them once, while it has none.
	if !settingsFileHas("terminal") {
		if u.DiffSplit != nil {
			a.diffSplit = *u.DiffSplit
			a.settings.Editor.DiffSplit = a.diffSplit
		}
		if u.FontSize >= 8 && u.FontSize <= 32 {
			a.fontSize = u.FontSize
			a.settings.Terminal.FontSize = a.fontSize
		}
	}
	a.agentSetupAsked = u.AgentSetupAsked
	a.settingsPage = u.SettingsPage
	a.wtAgent = u.WorktreeAgent
	if u.WorktreeEnv != nil {
		a.wtEnv = *u.WorktreeEnv
	}
	if u.MergeSquash != nil {
		a.mergeSquash = *u.MergeSquash
	}
}

// saveUIPrefs writes ui.json, as the window closes.
func (a *appState) saveUIPrefs() {
	if a.win == nil {
		return
	}
	u := uiPrefs{
		SidebarVisible: &a.sidebarVisible, SidebarW: a.sidebarW,
		InspectorVisible: &a.inspectorVisible, InspectorW: a.inspectorW,
		InspectorTab:    &a.inspectorTab,
		AgentSetupAsked: a.agentSetupAsked,
		SettingsPage:    a.settingsPage,
		WorktreeAgent:   a.wtAgent, WorktreeEnv: &a.wtEnv, MergeSquash: &a.mergeSquash,
	}
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return
	}
	path := uiPrefsPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		os.Rename(tmp, path)
	}
}
