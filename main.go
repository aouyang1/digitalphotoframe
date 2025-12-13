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
	// Get ROOT_PATH_DPF from environment
	rootPath := os.Getenv("ROOT_PATH_DPF")
	if rootPath == "" {
		log.Fatal("ROOT_PATH_DPF environment variable is required")
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
		case <-remoteManager.Updated:
			slog.Info("found new updates to remote, restarting slideshow")
			mu.Lock()
			restartSlideshow()
			mu.Unlock()
		case <-localManager.Updated:
			slog.Info("found new updates to local, restarting slideshow")
			mu.Lock()
			restartSlideshow()
			mu.Unlock()
		}
	}
}
