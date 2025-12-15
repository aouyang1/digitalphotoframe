package main

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	mapset "github.com/deckarep/golang-set/v2"
)

var supportedExt = mapset.NewSet(
	".jpeg", ".jpg", ".JPEG", ".JPG",
	".png", ".PNG",
)

func main() {
	// Get DPF_ROOT_PATH from environment
	rootPath := os.Getenv("DPF_ROOT_PATH")
	if rootPath == "" {
		log.Fatal("DPF_ROOT_PATH environment variable is required")
	}

	// Initialize database
	dbPath := filepath.Join(rootPath, "photos.db")
	database, err := NewDatabase(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Initialize and start web server
	webServer := NewWebServer(database, rootPath)
	go webServer.Start("0.0.0.0:8080")

	// Start slideshow
	imgPaths, err := webServer.getImgPaths()
	if err != nil {
		log.Fatalf("Failed to get image paths: %v", err)
	}

	interval := 15
	if err := restartSlideshow(imgPaths, interval); err != nil {
		slog.Error("Failed to start slideshow on initialization, continuing", "error", err)
	}

	// Initialize remote manager
	remoteManager, err := NewRemoteManager()
	if err != nil {
		log.Fatalf("Failed to initialize remote manager: %v", err)
	}

	go remoteManager.Run()

	// Initialize local manager
	localManager, err := NewLocalManager()
	if err != nil {
		log.Fatalf("Failed to initialize local manager: %v", err)
	}

	go localManager.Run()

	var mu sync.Mutex
	for {
		select {
		case <-webServer.Updated:
		case <-remoteManager.Updated:
		case <-localManager.Updated:
			slog.Info("found new updates, restarting slideshow")
			mu.Lock()
			imgPaths, err := webServer.getImgPaths()
			if err != nil {
				slog.Error("error while getting image paths", "error", err)
				mu.Unlock()
				continue
			}
			if err := restartSlideshow(imgPaths, interval); err != nil {
				slog.Error("error while restarting slideshow from web upload", "error", err)
			}
			mu.Unlock()
		}
	}
}
