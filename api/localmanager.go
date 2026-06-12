package api

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aouyang1/digitalphotoframe/api/client"
	"github.com/aouyang1/digitalphotoframe/util"
	mapset "github.com/deckarep/golang-set/v2"
)

const (
	localCheckInterval = 24 * time.Hour
	localPhotoLimit    = 1000
)

type LocalManager struct {
	path string

	photoClient  *client.PhotoClient
	trackedFiles mapset.Set[string]

	Updated chan bool
}

func NewLocalManager() (*LocalManager, error) {
	// Use DPF_ROOT_PATH/original if set
	rootPath := os.Getenv("DPF_ROOT_PATH")
	var path string
	if rootPath != "" {
		path = filepath.Join(rootPath, "original")
	} else {
		path = "."
	}

	// Initialize photo client
	photoClient := client.NewPhotoClient(webServerURL)

	l := &LocalManager{
		path:         path,
		photoClient:  photoClient,
		trackedFiles: mapset.NewSet[string](),
		Updated:      make(chan bool),
	}

	currentFiles, _, err := l.getCurrentFiles()
	if err != nil {
		slog.Warn("error reading local directory on initialization", "path", l.path, "error", err)
		return nil, err
	}

	l.trackedFiles = currentFiles

	return l, nil
}

type fileInfo struct {
	name    string
	modTime time.Time
	path    string
}

func (l *LocalManager) getCurrentFiles() (mapset.Set[string], []fileInfo, error) {
	dirs, err := os.ReadDir(l.path)
	if err != nil {
		return nil, nil, err
	}

	currentFiles := mapset.NewSet[string]()
	var fileInfos []fileInfo

	for _, dir := range dirs {
		name := dir.Name()
		ext := filepath.Ext(name)
		if !util.SupportedExt.Contains(ext) {
			continue
		}

		currentFiles.Add(name)

		info, err := dir.Info()
		if err != nil {
			continue
		}

		fileInfos = append(fileInfos, fileInfo{
			name:    name,
			modTime: info.ModTime(),
			path:    filepath.Join(l.path, name),
		})
	}

	return currentFiles, fileInfos, nil
}

func (l *LocalManager) Run() {
	ticker := time.NewTicker(localCheckInterval)

	// Initial scan
	l.scanAndRegister()

	for range ticker.C {
		l.scanAndRegister()
	}
}

func (l *LocalManager) scanAndRegister() {
	currentFiles, fileInfos, err := l.getCurrentFiles()
	if err != nil {
		slog.Warn("error reading local directory", "path", l.path, "error", err)
		return
	}

	// Find new files
	newFiles := currentFiles.Difference(l.trackedFiles).ToSlice()

	// check if we have new photos
	hasNewFiles := false
	for _, name := range newFiles {
		// Find the file info
		var fileInfo *fileInfo
		for i := range fileInfos {
			if fileInfos[i].name == name {
				fileInfo = &fileInfos[i]
				break
			}
		}

		if fileInfo == nil {
			continue
		}

		hasNewFiles = true
	}

	// Update tracked files
	l.trackedFiles = currentFiles

	// Ensure all local files are registered
	for _, name := range currentFiles.ToSlice() {
		// Find the file info
		var fileInfo *fileInfo
		for i := range fileInfos {
			if fileInfos[i].name == name {
				fileInfo = &fileInfos[i]
				break
			}
		}

		if fileInfo == nil {
			continue
		}

		if err := l.photoClient.RegisterPhotoIfNotExists(fileInfo.path, 1); err != nil {
			slog.Warn("error while registering local photo", "name", name, "error", err)
		}
	}

	// Get all registered category 1 photos from DB and compare with local files
	registeredPhotos, err := l.photoClient.GetPhotos(1)
	if err != nil {
		slog.Warn("error getting registered photos from DB", "error", err)
	} else {
		// Create set of registered photo names
		registeredNames := mapset.NewSet[string]()
		for _, photo := range registeredPhotos {
			registeredNames.Add(photo.PhotoName)
		}

		// Find photos registered in DB but not present locally
		toDeregister := registeredNames.Difference(currentFiles).ToSlice()
		if len(toDeregister) > 0 {
			slog.Info("deregistering category 1 photos not present locally", "count", len(toDeregister), "names", toDeregister)
			for _, name := range toDeregister {
				if err := l.photoClient.DeletePhoto(name, 1); err != nil {
					slog.Warn("error while deregistering photo", "name", name, "error", err)
				}
			}
		}
	}

	// Check if we need to enforce limit
	if currentFiles.Cardinality() > localPhotoLimit {
		// Sort by modification time (oldest first)
		sort.Slice(fileInfos, func(i, j int) bool {
			return fileInfos[i].modTime.Before(fileInfos[j].modTime)
		})

		// Remove oldest files until we're under the limit
		toRemove := currentFiles.Cardinality() - localPhotoLimit
		for i := 0; i < toRemove && i < len(fileInfos); i++ {
			oldest := fileInfos[i]
			if err := os.Remove(oldest.path); err != nil {
				slog.Warn("unable to remove old file", "name", oldest.name, "error", err)
			} else {
				slog.Info("removed old file to enforce limit", "name", oldest.name)
				l.trackedFiles.Remove(oldest.name)
				hasNewFiles = true
			}
		}
	}

	// Signal update if files changed
	if hasNewFiles || len(newFiles) > 0 {
		select {
		case l.Updated <- true:
		default:
			// Channel is full, skip
		}
	}
}
