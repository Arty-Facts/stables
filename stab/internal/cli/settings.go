package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/stables/stab/internal/config"
)

// cmdSettings implements `stab settings`: view and edit the local
// configuration (default image, harness command, network policy) and list
// active workspaces.
func (a *App) cmdSettings(ctx context.Context) int {
	for {
		fmt.Fprintln(a.Out, "=== stab settings ===")
		fmt.Fprintf(a.Out, "  state dir       : %s\n", a.Config.StateDirPath(a.Home))
		fmt.Fprintf(a.Out, "  default image   : %s\n", a.Config.DefaultImage)
		fmt.Fprintf(a.Out, "  harness command : %s\n", a.Config.HarnessCommand)
		fmt.Fprintf(a.Out, "  network policy  : %s\n", a.Config.Profile.Network)
		fmt.Fprintf(a.Out, "  gpu             : %s\n", gpuLabel(a.Config.Profile.GPU))
		fmt.Fprintln(a.Out)
		fmt.Fprintln(a.Out, " 1) set default image")
		fmt.Fprintln(a.Out, " 2) set harness command")
		fmt.Fprintln(a.Out, " 3) set network policy (deny/bridge/allowlist/internet)")
		fmt.Fprintln(a.Out, " 4) toggle gpu (auto/off/all)")
		fmt.Fprintln(a.Out, " 5) list workspaces")
		fmt.Fprintln(a.Out, " q) quit")
		choice, err := a.prompt("choice >")
		if err != nil || strings.TrimSpace(strings.ToLower(choice)) == "q" {
			return 0
		}
		switch strings.TrimSpace(choice) {
		case "1":
			a.settingsSetImage()
		case "2":
			a.settingsSetHarness()
		case "3":
			a.settingsSetNetwork()
		case "4":
			a.settingsToggleGPU()
		case "5":
			a.settingsListWorkspaces()
		default:
			fmt.Fprintln(a.Out, "unknown choice")
		}
	}
}

func gpuLabel(gpu string) string {
	switch gpu {
	case "":
		return "auto"
	case "off":
		return "off"
	default:
		return gpu
	}
}

func (a *App) settingsToggleGPU() {
	switch a.Config.Profile.GPU {
	case "":
		a.Config.Profile.GPU = "off"
	case "off":
		a.Config.Profile.GPU = "all"
	default:
		a.Config.Profile.GPU = ""
	}
	if err := a.Config.Save(a.Home); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return
	}
	a.Projects.Config = a.Config
	fmt.Fprintf(a.Out, "gpu: %s\n", gpuLabel(a.Config.Profile.GPU))
}

func (a *App) settingsSetImage() {
	v, err := a.prompt(fmt.Sprintf("default image [%s]:", a.Config.DefaultImage))
	if err != nil || v == "" {
		return
	}
	a.Config.DefaultImage = v
	if err := a.Config.Save(a.Home); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return
	}
	// Rebind the controller to the updated config.
	a.Projects.Config = a.Config
	fmt.Fprintln(a.Out, "saved")
}

func (a *App) settingsSetHarness() {
	v, err := a.prompt(fmt.Sprintf("harness command [%s]:", a.Config.HarnessCommand))
	if err != nil || v == "" {
		return
	}
	a.Config.HarnessCommand = v
	if err := a.Config.Save(a.Home); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return
	}
	a.Projects.Config = a.Config
	fmt.Fprintln(a.Out, "saved")
}

func (a *App) settingsSetNetwork() {
	v, err := a.prompt(fmt.Sprintf("network policy [%s]:", a.Config.Profile.Network))
	if err != nil || v == "" {
		return
	}
	switch v {
	case config.NetworkDeny, config.NetworkBridge, config.NetworkAllowlist, config.NetworkInternet:
		a.Config.Profile.Network = v
	default:
		fmt.Fprintln(a.Out, "invalid policy; use deny, bridge, allowlist, or internet")
		return
	}
	if err := a.Config.Save(a.Home); err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return
	}
	a.Projects.Config = a.Config
	fmt.Fprintln(a.Out, "saved")
}

func (a *App) settingsListWorkspaces() {
	projects, err := a.Store.ListProjects()
	if err != nil {
		fmt.Fprintln(a.ErrOut, "stab:", err)
		return
	}
	if len(projects) == 0 {
		fmt.Fprintln(a.Out, "(no workspaces)")
		return
	}
	fmt.Fprintf(a.Out, "%-12s %-24s %s\n", "ID", "NAME", "ROOT")
	for _, p := range projects {
		fmt.Fprintf(a.Out, "%-12s %-24s %s\n", p.ID, p.Name, p.RootPath)
	}
}
