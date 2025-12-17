// Package slideshow manages the starting and stopping of the slideshow imv app
package slideshow

import (
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/aouyang1/digitalphotoframe/util"
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

type rotateOptions struct {
	name    string
	degrees int
	scale   int
}

func rotateImages(rootPath string, targetMaxDim int) error {
	dirs := []string{
		filepath.Join(rootPath, "original"),
		filepath.Join(rootPath, "original/surprise"),
	}

	photosDirs := []string{
		filepath.Join(rootPath, "photos"),
		filepath.Join(rootPath, "photos/surprise"),
	}
	for i, dir := range dirs {
		// Check if directory exists and has files
		entries, err := os.ReadDir(dir)
		if err != nil {
			slog.Debug("directory does not exist or is empty, skipping rotation", "dir", dir)
			continue
		}

		photosEntries, err := os.ReadDir(photosDirs[i])
		if err != nil {
			slog.Debug("photos directory does not exist or is empty, skipping rotation", "dir", dir)
			continue
		}

		// Collect image files (excluding already rotated ones) and build rotate options
		var imageRotOptions []rotateOptions
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()

			// Skip already rotated files
			if strings.Contains(name, "_IMGP.") {
				continue
			}

			// check if already rotated into photos directory
			alreadyRotated := false
			for _, photoEntry := range photosEntries {
				if strings.HasPrefix(photoEntry.Name(), strings.Trim(name, filepath.Ext(name))) {
					alreadyRotated = true
					break
				}
			}
			if alreadyRotated {
				continue
			}

			// perform rotation in original directory
			ext := filepath.Ext(name)
			if util.SupportedExt.Contains(ext) {
				imageFilePath := filepath.Join(dir, name)
				imageFile, err := os.Open(imageFilePath)
				if err != nil {
					slog.Warn("unable to read image for resolution", "name", imageFilePath, "error", err)
					continue
				}
				var imageCfg image.Config
				switch strings.ToLower(ext) {
				case ".jpg", ".jpeg":
					imageCfg, err = jpeg.DecodeConfig(imageFile)
				case ".png":
					imageCfg, err = png.DecodeConfig(imageFile)
				default:
					slog.Warn("unknown file extension to get resolution details", "ext", ext, "name", imageFilePath)
				}
				if err != nil {
					slog.Warn("unable to read image config", "name", imageFilePath, "error", err)
				}
				downScale := 100
				downScale = min(downScale, int(float64(targetMaxDim)/float64(imageCfg.Height)*100))
				downScale = min(downScale, int(float64(targetMaxDim)/float64(imageCfg.Width)*100))

				rOpt := rotateOptions{
					name:    imageFilePath,
					degrees: 90,
					scale:   downScale,
				}
				imageRotOptions = append(imageRotOptions, rOpt)
			}
		}

		if len(imageRotOptions) == 0 {
			continue
		}

		// downsize and then rotate
		for rOpt := range slices.Values(imageRotOptions) {
			args := append([]string{"-x", strconv.Itoa(rOpt.scale) + "%"}, rOpt.name)
			cmd := exec.Command("imgp", args...)
			if err := cmd.Run(); err != nil {
				slog.Warn("failed to downsize image", "name", rOpt.name, "error", err)
				continue
			}

			ext := filepath.Ext(rOpt.name)
			baseName := filepath.Base(rOpt.name)
			baseName = strings.TrimSuffix(baseName, ext)
			imgpName := filepath.Join(filepath.Dir(rOpt.name), baseName+"_IMGP"+ext)

			args = append([]string{"-o", strconv.Itoa(rOpt.degrees), "-w", "-i"}, imgpName)
			cmd = exec.Command("imgp", args...)
			if err := cmd.Run(); err != nil {
				slog.Warn("failed to rotate image", "name", imgpName, "error", err)
			}
		}
	}

	// clean up any rotated images in final output if they are not present in original
	for i, dir := range photosDirs {
		// Check if directory exists and has files
		photosEntries, err := os.ReadDir(dir)
		if err != nil {
			slog.Debug("directory does not exist or is empty, skipping rotation", "dir", dir)
			continue
		}

		entries, err := os.ReadDir(dirs[i])
		if err != nil {
			slog.Debug("photos directory does not exist or is empty, skipping rotation", "dir", dir)
			continue
		}

		for _, photoEntry := range photosEntries {
			if photoEntry.IsDir() {
				continue
			}
			photoName := photoEntry.Name()

			// check if final file is not present in original
			found := false
			for _, entry := range entries {
				if strings.HasPrefix(photoName, strings.Trim(entry.Name(), filepath.Ext(entry.Name()))) {
					found = true
					break
				}
			}
			if found {
				continue
			}

			// delete orphaned image
			os.Remove(filepath.Join(dir, photoName))
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
	if err != nil {
		slog.Error("unable to move directory files", "src", srcDir, "dst", dstDir, "error", err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.Contains(name, "_IMGP.") {
			continue
		}

		src := filepath.Join(srcDir, name)
		dst := filepath.Join(dstDir, name)
		if err := os.Rename(src, dst); err != nil {
			slog.Warn("failed to move rotated image", "src", src, "dst", dst, "error", err)
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

const defaultInterval = 15

func startImvWayland(rootPath string, imgPaths []string, interval int) error {
	// Start imv-wayland in background
	args := []string{"-f", "-s", "full"}

	// set slideshow interval
	if interval <= 0 {
		interval = defaultInterval
	}
	args = append(args, "-t", strconv.Itoa(interval))

	// set explicit order of images or use default ordering by directory
	if len(imgPaths) > 0 {
		args = append(args, imgPaths...)
	} else {
		slog.Info("no explicit order specified, using default directory ordering for imv")
		photosDir := filepath.Join(rootPath, "photos")

		// Ensure photos directory exists
		if err := os.MkdirAll(photosDir, 0755); err != nil {
			return fmt.Errorf("failed to create photos directory: %w", err)
		}

		args = append(args, "-r", photosDir)
	}

	cmd := exec.Command("/usr/bin/imv-wayland", args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start imv-wayland: %w", err)
	}

	slog.Info("started imv-wayland slideshow")
	return nil
}

const defaultTargetMaxDim = 1080

func RestartSlideshow(imgPaths []string, interval int) error {
	rootPath := os.Getenv("DPF_ROOT_PATH")
	if rootPath == "" {
		return errors.New("DPF_ROOT_PATH environment variable is required")
	}
	targetMaxDimStr := os.Getenv("DPF_TARGET_MAX_DIM")
	targetMaxDim, err := strconv.Atoi(targetMaxDimStr)
	if err != nil {
		slog.Warn("unable to parse DPF_TARGET_MAX_DIM, using default", "DPF_TARGET_MAX_DIM", targetMaxDimStr, "default", defaultTargetMaxDim)
		targetMaxDim = defaultTargetMaxDim
	}

	// Clear old imgp artifacts
	if err := clearImgpArtifacts(rootPath); err != nil {
		return fmt.Errorf("error clearing imgp artifacts, %w", err)
	}

	// Rotate images
	if err := rotateImages(rootPath, targetMaxDim); err != nil {
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
	if err := startImvWayland(rootPath, imgPaths, interval); err != nil {
		return fmt.Errorf("failed to restart slideshow: %w", err)
	}

	return nil
}
