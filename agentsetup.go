package main

import (
	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/cli"
	"omaterm/internal/session"
)

// An agent tells omaterm its conversation through a hook, which `omaterm
// skill setup` installs; without one, a restart continues only the last
// conversation of the pane's folder. The first agent seen without its
// hook offers, once, to install them.

// offerAgentSetup notices an agent running without its hook. UI thread.
func (a *appState) offerAgentSetup(infos map[string]session.Info) {
	if a.agentSetupAsked || a.agentSetupOffer != "" {
		return
	}
	for _, i := range infos {
		if i.Agent == "" || i.AgentSession != nil {
			continue
		}
		installed, ok := a.hookChecked[i.Agent]
		if !ok {
			installed = cli.HookInstalled(i.Agent)
			a.hookChecked[i.Agent] = installed
		}
		if !installed {
			a.agentSetupOffer = i.Agent
			return
		}
	}
}

// agentSetupBanner offers to set up the agent integration.
func (a *appState) agentSetupBanner(c *ui.Context) {
	if a.agentSetupOffer == "" || a.agentSetupAsked {
		return
	}
	name := agent.DisplayName(a.agentSetupOffer)
	ui.Row(c.Key("agent-setup-banner")).Margin(0, 0, a.paneGap(), 0).Padding(8, 12).Gap(10).AlignItems(ui.Center).
		Radius(8).Background(pal.Raised).Border(1, pal.Border).Children(func() {
		ui.Icon(c, iconRefresh).Width(14).Height(14).TextColor(pal.Accent)
		msg := "Resume " + name + " in its own conversation after a restart: set up the agent integration."
		// Two lines at most, in a narrow window; the tooltip has it all.
		ui.Text(c, msg).Tooltip(msg).
			FontSize(12).TextColor(pal.TextSoft).MaxLines(2).Ellipsis("…").Grow(1).MinWidth(0)
		later := ui.ButtonBase(c).Padding(4, 10).Radius(6).Height(26).Children(func() {
			ui.Text(c, "Not Now").FontSize(12).TextColor(pal.TextMuted)
		})
		if later.Clicked() {
			a.answerAgentSetup()
		}
		if ui.PrimaryButton(c, "Set Up").Height(26).FontSize(12).Clicked() {
			offered := a.agentSetupOffer
			a.answerAgentSetup()
			a.setupAgents(c, offered)
		}
	})
}

// answerAgentSetup keeps that the offer was answered, at once: a logout
// may end the app before it closes.
func (a *appState) answerAgentSetup() {
	a.agentSetupAsked, a.agentSetupOffer = true, ""
	a.saveUIPrefs()
}

// setupAgents installs omaterm's skill and the agents' hooks, as `omaterm
// skill setup` does, and says what is left to do for the agent named.
func (a *appState) setupAgents(c *ui.Context, name string) {
	for _, k := range []string{"claude", "agents"} {
		if err := cli.InstallSkill(k); err != nil {
			toast(c, "Agent setup failed: "+err.Error())
			return
		}
	}
	a.hookChecked = map[string]bool{}
	msg := "Agents started in omaterm now resume their own conversation after a restart"
	if note := cli.HookNotes[name]; note != "" {
		msg += ". " + note
	}
	toast(c, msg)
}
