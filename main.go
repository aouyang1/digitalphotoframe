package main

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/aouyang1/digitalphotoframe/api"
	"github.com/aouyang1/digitalphotoframe/slideshow"
	"github.com/aouyang1/digitalphotoframe/store"
)

func main() {
	// Get DPF_ROOT_PATH from environment
	rootPath := os.Getenv("DPF_ROOT_PATH")
	if rootPath == "" {
		log.Fatal("DPF_ROOT_PATH environment variable is required")
	}

	// Initialize database
	dbPath := filepath.Join(rootPath, "photos.db")
	database, err := store.NewDatabase(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Initialize and start web server
	webServer := api.NewWebServer(database, rootPath)

	// wait for graphics to load up. maybe there's something we can check on the pi to ensure
	// the graphics hardware is ready before starting imv
	time.Sleep(5 * time.Second)

	// Start slideshow
	imgPaths, err := webServer.GetImgPaths()
	if err != nil {
		log.Fatalf("Failed to get image paths: %v", err)
	}

	settings, err := webServer.GetAppSettings()
	if err != nil {
		log.Fatalf("error while getting settings, %v", err)
	}

	if err := slideshow.RestartSlideshow(imgPaths, settings.SlideshowIntervalSeconds); err != nil {
		slog.Warn("Failed to start slideshow on initialization, continuing", "error", err)
	}

	webServer.Start("0.0.0.0:80")
}
