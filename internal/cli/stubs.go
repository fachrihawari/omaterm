package cli

import "omaterm/internal/client"

// diffCmd prints a pane's changes (Phase 5).
func diffCmd(c *client.Client, args []string) error {
	return usageErr("omaterm diff is not available yet")
}

// browserCmd drives a browser pane (Phase 4).
func browserCmd(c *client.Client, args []string) error {
	return usageErr("omaterm browser is not available yet")
}
