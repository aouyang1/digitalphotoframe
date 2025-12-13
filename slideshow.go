package main

import (
	"log/slog"
	"os/exec"
)

func restartSlideshow() error {
	cmd := exec.Command("run.sh")
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	slog.Info("restart slideshow", "output", out)
	return nil
}
