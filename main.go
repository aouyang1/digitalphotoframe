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
	if err := restartSlideshow(); err != nil {
		slog.Error("error while starting slideshow on initialization", "error", err)
	}

	// Initialize remote manager
	remoteManager, err := NewRemoteManager()
	if err != nil {
		log.Fatal(err)
	}

	go remoteManager.Run()

	// Initialize local manager
	localManager, err := NewLocalManager()
	if err != nil {
		log.Fatal(err)
	}

	go localManager.Run()

	var mu sync.Mutex
	for {
		select {
		case <-webServer.Updated:
			slog.Info("found new updates from web upload, restarting slideshow")
			mu.Lock()
			if err := restartSlideshow(); err != nil {
				slog.Error("error while restarting slideshow from web upload", "error", err)
			}
			mu.Unlock()
		case <-remoteManager.Updated:
			slog.Info("found new updates to remote, restarting slideshow")
			mu.Lock()
			if err := restartSlideshow(); err != nil {
				slog.Error("error while restarting slideshow from remote photo update", "error", err)
			}
			mu.Unlock()
		case <-localManager.Updated:
			slog.Info("found new updates to local, restarting slideshow")
			mu.Lock()
			if err := restartSlideshow(); err != nil {
				slog.Error("error while restarting slideshow from local photo update", "error", err)
			}
			mu.Unlock()
		}
	}
}
