package wlrrandr

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

const OutputName = "HDMI-A-1"

type Output struct {
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Make         string       `json:"make"`
	Model        string       `json:"model"`
	Serial       string       `json:"serial"`
	PhysicalSize PhysicalSize `json:"physical_size"`
	Enabled      bool         `json:"enabled"`
	Modes        []Mode       `json:"modes"`
	Position     Position     `json:"position"`
	Transform    string       `json:"transform"`
	Scale        float64      `json:"scale"`
	AdaptiveSync bool         `json:"adaptive_sync"`
}

type PhysicalSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Mode struct {
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Refresh   float64 `json:"refresh"`
	Preferred bool    `json:"preferred"`
	Current   bool    `json:"current"`
}

type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// GetDisplayEnabled inspects the current state of the HDMI-A-1 output using wlr-randr.
// It returns true if the output is enabled, false if disabled.
func GetDisplayEnabled() (bool, error) {
	cmd := exec.Command("wlr-randr", "--output", OutputName, "--json")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to run wlr-randr: %w", err)
	}

	var results []Output
	if err := json.Unmarshal(out, &results); err != nil {
		return false, fmt.Errorf("failed to unmarshal wlr-randr output: %w", err)
	}

	for _, result := range results {
		if result.Name == OutputName {
			return result.Enabled, nil
		}
	}

	return false, fmt.Errorf("output %s not found", OutputName)
}

// UpdateDisplayEnabled updates the enabled state of the HDMI-A-1 output using wlr-randr.
func UpdateDisplayEnabled(enabled bool) error {
	arg := "--off"
	if enabled {
		arg = "--on"
	}
	cmd := exec.Command("wlr-randr", "--output", OutputName, arg)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run wlr-randr: %w", err)
	}
	return nil
}
