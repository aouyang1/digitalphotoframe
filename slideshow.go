package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func clearImgpArtifacts(rootPath string) error {
	dirs := []string{
		filepath.Join(rootPath, "original"),
		filepath.Join(rootPath, "original/surprise"),
	}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Directory might not exist, skip
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			// Check if file matches *_IMGP.* pattern
			if strings.Contains(name, "_IMGP.") {
				filePath := filepath.Join(dir, name)
				if err := os.Remove(filePath); err != nil {
					slog.Warn("failed to remove imgp artifact", "path", filePath, "error", err)
				} else {
					slog.Debug("removed imgp artifact", "path", filePath)
				}
			}
		}
	}

	return nil
}

func rotateImages(rootPath string) error {
	dirs := []string{
		filepath.Join(rootPath, "original"),
		filepath.Join(rootPath, "original/surprise"),
	}

	for _, dir := range dirs {
		// Check if directory exists and has files
		entries, err := os.ReadDir(dir)
		if err != nil {
			slog.Debug("directory does not exist or is empty, skipping rotation", "dir", dir)
			continue
		}

		// Collect image files (excluding already rotated ones)
		var imageFiles []string
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			// Skip already rotated files
			if strings.Contains(name, "_IMGP.") {
				continue
			}
			ext := filepath.Ext(name)
			if supportedExt.Contains(ext) {
				imageFiles = append(imageFiles, filepath.Join(dir, name))
			}
		}

		if len(imageFiles) == 0 {
			continue
		}

		// Run imgp -o 90 on all image files in directory
		args := append([]string{"-o", "90"}, imageFiles...)
		cmd := exec.Command("imgp", args...)
		if err := cmd.Run(); err != nil {
			slog.Warn("failed to rotate images", "dir", dir, "error", err)
			// Continue with other directories even if one fails
		} else {
			slog.Info("rotated images", "dir", dir, "count", len(imageFiles))
		}
	}

	return nil
}

func moveRotatedImages(rootPath string) error {
	// Move from original to photos
	originalDir := filepath.Join(rootPath, "original")
	photosDir := filepath.Join(rootPath, "photos")

	// Ensure photos directory exists
	if err := os.MkdirAll(photosDir, 0755); err != nil {
		return fmt.Errorf("failed to create photos directory: %w", err)
	}

	// Ensure photos/surprise directory exists
	surprisePhotosDir := filepath.Join(rootPath, "photos/surprise")
	if err := os.MkdirAll(surprisePhotosDir, 0755); err != nil {
		return fmt.Errorf("failed to create photos/surprise directory: %w", err)
	}

	// Move files from original
	moveDirFiles(originalDir, photosDir)

	// Move files from original/surprise
	surpriseDir := filepath.Join(rootPath, "original/surprise")
	moveDirFiles(surpriseDir, surprisePhotosDir)

	return nil
}

func moveDirFiles(srcDir, dstDir string) {
	entries, err := os.ReadDir(srcDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			if strings.Contains(name, "_IMGP.") {
				src := filepath.Join(srcDir, name)
				dst := filepath.Join(dstDir, name)
				if err := os.Rename(src, dst); err != nil {
					slog.Warn("failed to move rotated image", "src", src, "dst", dst, "error", err)
				} else {
					slog.Debug("moved rotated image", "from", src, "to", dst)
				}
			}
		}
	}
}

func killImvWayland() error {
	cmd := exec.Command("pkill", "imv-wayland")
	if err := cmd.Run(); err != nil {
		// pkill returns error if no process found, which is fine
		return fmt.Errorf("imv-wayland not running or already killed, %w", err)
	}
	return nil
}

func startImvWayland(rootPath string) error {
	photosDir := filepath.Join(rootPath, "photos")

	// Ensure photos directory exists
	if err := os.MkdirAll(photosDir, 0755); err != nil {
		return fmt.Errorf("failed to create photos directory: %w", err)
	}

	// Start imv-wayland in background
	cmd := exec.Command("/usr/bin/imv-wayland", "-f", "-s", "full", "-t", "15", "-r", photosDir)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start imv-wayland: %w", err)
	}

	slog.Info("started imv-wayland slideshow", "path", photosDir)
	return nil
}

func restartSlideshow() error {
	rootPath := os.Getenv("DPF_ROOT_PATH")
	if rootPath == "" {
		return errors.New("DPF_ROOT_PATH environment variable is required")
	}

	// Clear old imgp artifacts
	if err := clearImgpArtifacts(rootPath); err != nil {
		return fmt.Errorf("error clearing imgp artifacts, %w", err)
	}

	// Rotate images
	if err := rotateImages(rootPath); err != nil {
		return fmt.Errorf("error rotating images, %w", err)
	}

	// Move rotated images
	if err := moveRotatedImages(rootPath); err != nil {
		return fmt.Errorf("error moving rotated images, %w", err)
	}

	// Kill existing imv-wayland
	if err := killImvWayland(); err != nil {
		slog.Info("error killing imv-wayland", "error", err)
	}

	// Start new imv-wayland
	if err := startImvWayland(rootPath); err != nil {
		return fmt.Errorf("failed to restart slideshow: %w", err)
	}

	return nil
}
