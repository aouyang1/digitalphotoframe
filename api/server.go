// Package api is the main api web server
package api

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/aouyang1/digitalphotoframe/api/models"
	"github.com/aouyang1/digitalphotoframe/api/web/templates"
	"github.com/aouyang1/digitalphotoframe/display"
	"github.com/aouyang1/digitalphotoframe/slideshow"
	"github.com/aouyang1/digitalphotoframe/store"
	"github.com/aouyang1/digitalphotoframe/util"
	"github.com/gin-gonic/gin"
)

//go:embed web/templates/* web/static/**
var webFiles embed.FS

const webServerURL = "http://localhost:80"

type ServerError struct {
	StatusCode int
	Error      error
}

type WebServer struct {
	router   *gin.Engine
	db       *store.Database
	rootPath string

	localManager    *LocalManager
	remoteManager   *RemoteManager
	scheduleManager *ScheduleManager

	Updated chan bool

	// this ensures only one go routine can restart the slideshow at a time
	imvMutex sync.Mutex
}

func NewWebServer(db *store.Database, rootPath string) *WebServer {
	router := gin.Default()

	ws := &WebServer{
		router:   router,
		db:       db,
		rootPath: rootPath,
		Updated:  make(chan bool),
	}

	localManager, err := NewLocalManager()
	if err != nil {
		log.Fatalf("Failed to initialize local manager: %v", err)
	}
	remoteManager, err := NewRemoteManager()
	if err != nil {
		log.Fatalf("Failed to initialize remote manager: %v", err)
	}
	scheduleManager, err := NewScheduleManager(db)
	if err != nil {
		log.Fatalf("Failed to initialize schedule manager: %v", err)
	}
	ws.localManager = localManager
	ws.remoteManager = remoteManager
	ws.scheduleManager = scheduleManager

	// Setup routes
	ws.setupRoutes()

	return ws
}

func (ws *WebServer) setupRoutes() {
	// Create filesystem for static files (strip "web/" prefix)
	staticFS, err := fs.Sub(webFiles, "web/static")
	if err != nil {
		log.Fatalf("Failed to create static filesystem: %v", err)
	}

	// Create filesystem for templates
	templatesFS, err := fs.Sub(webFiles, "web/templates")
	if err != nil {
		log.Fatalf("Failed to create templates filesystem: %v", err)
	}

	// Serve static files from embedded filesystem
	ws.router.StaticFS("static", http.FS(staticFS))

	// Serve favicon
	ws.router.GET("/favicon.ico", func(c *gin.Context) {
		c.Header("Content-Type", "image/svg+xml")
		data, err := webFiles.ReadFile("web/static/images/favicon.svg")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "image/svg+xml", data)
	})
	ws.router.GET("/favicon.svg", func(c *gin.Context) {
		c.Header("Content-Type", "image/svg+xml")
		data, err := webFiles.ReadFile("web/static/images/favicon.svg")
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "image/svg+xml", data)
	})

	// Serve index.html from embedded filesystem
	ws.router.GET("/", func(c *gin.Context) {
		data, err := fs.ReadFile(templatesFS, "index.html")
		if err != nil {
			slog.Error("failed to read index.html", "error", err)
			c.String(http.StatusInternalServerError, "Failed to load index.html")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", data)
	})
	ws.router.GET("/ui/photos/:category", ws.handleUIPhotos)

	// API routes
	ws.router.POST("/upload", ws.handleUpload)
	ws.router.POST("/photos/register", ws.handleRegisterPhoto)
	ws.router.GET("/photos", ws.handleListPhotos)
	ws.router.GET("/photos/:category/:name/image", ws.handlePhotoImage)
	ws.router.DELETE("/photos/:name/category/:category", ws.handleDeletePhoto)
	ws.router.POST("/slideshow/play/:name/category/:category", ws.handlePlayFromPhoto)
	ws.router.GET("/settings", ws.handleGetSettings)
	ws.router.PUT("/settings", ws.handleUpdateSettings)
	ws.router.GET("/schedule", ws.handleGetSchedule)
	ws.router.PUT("/schedule", ws.handleUpdateSchedule)
	ws.router.GET("/display", ws.handleGetDisplay)
	ws.router.PUT("/display/:state", ws.handleUpdateDisplay)
}

func (ws *WebServer) Start(port string) {
	// listen for updates and restart the slideshow
	go func() {
		for {
			select {
			case <-ws.Updated:
			case <-ws.remoteManager.Updated:
			case <-ws.localManager.Updated:
				slog.Info("found new updates, restarting slideshow")
				ws.imvMutex.Lock()
				imgPaths, err := ws.GetImgPaths()
				if err != nil {
					slog.Error("error while getting image paths", "error", err)
					ws.imvMutex.Unlock()
					continue
				}
				settings, err := ws.db.GetAppSettings()
				if err != nil {
					slog.Error("error while getting settings", "error", err)
					ws.imvMutex.Unlock()
					continue
				}
				if err := slideshow.RestartSlideshow(imgPaths, settings.SlideshowIntervalSeconds); err != nil {
					slog.Error("error while restarting slideshow from update", "error", err)
				}
				ws.imvMutex.Unlock()
			}
		}
	}()

	go ws.localManager.Run()
	go ws.remoteManager.Run()
	go ws.scheduleManager.Run()

	log.Printf("Starting web server on port %s", port)
	if err := ws.router.Run(port); err != nil {
		log.Fatalf("Failed to start web server: %v", err)
	}
}

func (ws *WebServer) getAllImages() ([]store.Photo, error) {
	allPhotos, err := ws.db.GetAllPhotos(0)
	if err != nil {
		return nil, fmt.Errorf("failed to get all photos for surprise category: %v", err)
	}
	allPhotosOriginal, err := ws.db.GetAllPhotos(1)
	if err != nil {
		return nil, fmt.Errorf("failed to get all photos for original category: %v", err)
	}
	allPhotos = append(allPhotos, allPhotosOriginal...)

	return allPhotos, nil
}

func (ws *WebServer) GetAppSettings() (*store.AppSettings, error) {
	return ws.db.GetAppSettings()
}

func (ws *WebServer) GetImgPaths() ([]string, error) {
	allPhotos, err := ws.getAllImages()
	if err != nil {
		return nil, fmt.Errorf("failed to get all images: %v", err)
	}
	imgPaths := make([]string, len(allPhotos))
	for i, photo := range allPhotos {
		imgPaths[i] = ws.buildImgPathFromPhoto(photo)
	}
	return imgPaths, nil
}

// buildImgPathFromPhoto constructs the filesystem path to the rotated (_IMGP) image
// corresponding to a Photo record, based on its category and the web server rootPath.
func (ws *WebServer) buildImgPathFromPhoto(photo store.Photo) string {
	baseName := strings.TrimSuffix(photo.PhotoName, filepath.Ext(photo.PhotoName))
	rotatedName := baseName + "_IMGP" + filepath.Ext(photo.PhotoName)

	switch photo.Category {
	case 0:
		return filepath.Join(ws.rootPath, "photos", "surprise", rotatedName)
	case 1:
		return filepath.Join(ws.rootPath, "photos", rotatedName)
	default:
		// Fallback: treat as original photos directory
		return filepath.Join(ws.rootPath, "photos", rotatedName)
	}
}

func (ws *WebServer) handleUpload(c *gin.Context) {
	// Check if this is an HTMX request
	isHTMX := c.GetHeader("HX-Request") == "true"

	if srvErr := ws.upload(c); srvErr != nil {
		if isHTMX {
			c.String(srvErr.StatusCode, srvErr.Error.Error())
			return
		}
		c.JSON(srvErr.StatusCode, models.ErrorResponse{Error: srvErr.Error.Error()})
		return
	}
	// If HTMX request, return HTML fragment with updated photos
	if isHTMX {
		// Get all photos for category 1
		photos, err := ws.db.GetAllPhotos(1)
		if err != nil {
			c.String(http.StatusInternalServerError, "failed to refresh photos")
			return
		}

		component := templates.PhotoRow(photos, 1)
		component.Render(c.Request.Context(), c.Writer)

		// trigger slideshow restart
		ws.Updated <- true
		return
	}

	c.Status(http.StatusOK)

	// trigger slideshow restart
	ws.Updated <- true
}

func (ws *WebServer) upload(c *gin.Context) *ServerError {
	// Get the file from the form
	file, err := c.FormFile("file")
	if err != nil {
		return &ServerError{http.StatusBadRequest, errors.New("no file provided")}
	}

	// Validate file extension
	ext := filepath.Ext(file.Filename)
	if !util.SupportedExt.Contains(ext) {
		return &ServerError{http.StatusBadRequest, fmt.Errorf("unsupported file extension: %s. Supported: .jpeg, .jpg, .png", ext)}
	}

	// Check for duplicates
	exists, err := ws.db.PhotoExists(file.Filename, 1)
	if err != nil {
		return &ServerError{http.StatusInternalServerError, fmt.Errorf("database error, %w", err)}
	}
	if exists {
		return &ServerError{http.StatusConflict, fmt.Errorf("photo with name '%s' already exists", file.Filename)}
	}

	// Ensure the original directory exists
	originalDir := filepath.Join(ws.rootPath, "original")
	if err := os.MkdirAll(originalDir, 0o755); err != nil {
		return &ServerError{http.StatusInternalServerError, fmt.Errorf("failed to create directory: %w", err)}
	}

	// Save file to disk
	filePath := filepath.Join(originalDir, file.Filename)
	if err := c.SaveUploadedFile(file, filePath); err != nil {
		return &ServerError{http.StatusInternalServerError, fmt.Errorf("failed to save file: %w", err)}
	}

	// auto resize so that viewing in ui is more reliable
	targetMaxDimStr := os.Getenv("DPF_TARGET_MAX_DIM")
	targetMaxDim, err := strconv.Atoi(targetMaxDimStr)
	if err != nil {
		slog.Warn("unable to parse DPF_TARGET_MAX_DIM, using default", "DPF_TARGET_MAX_DIM", targetMaxDimStr, "default", slideshow.DefaultTargetMaxDim)
		targetMaxDim = slideshow.DefaultTargetMaxDim
	}

	rOpt, err := slideshow.GenerateRotateOptions(originalDir, file.Filename, targetMaxDim)
	if err != nil {
		slog.Warn("unable generate rotate options", "error", err)
	} else {
		args := append([]string{"-w", "-x", strconv.Itoa(rOpt.Scale) + "%"}, rOpt.Name)
		cmd := exec.Command("imgp", args...)
		if err := cmd.Run(); err != nil {
			slog.Warn("failed to downsize image", "name", rOpt.Name, "error", err)
		}
	}

	// Get max order for category 1 (original)
	maxOrder, err := ws.db.GetMaxOrder(1)
	if err != nil {
		// Clean up file if DB insert fails
		if remErr := os.Remove(filePath); remErr != nil {
			return &ServerError{http.StatusInternalServerError, fmt.Errorf("database error, %w, with failed file removal, %w", err, remErr)}
		}
		return &ServerError{http.StatusInternalServerError, fmt.Errorf("database error: %v", err)}
	}

	// Insert into database
	if err := ws.db.InsertPhoto(file.Filename, 1, maxOrder); err != nil {
		// Clean up file if DB insert fails
		if remErr := os.Remove(filePath); remErr != nil {
			return &ServerError{http.StatusInternalServerError, fmt.Errorf("failed to insert photo into database, %w, with failed file removal, %w", err, remErr)}
		}

		return &ServerError{http.StatusInternalServerError, fmt.Errorf("failed to insert photo into database: %w", err)}
	}
	return nil
}

func (ws *WebServer) handleRegisterPhoto(c *gin.Context) {
	// Parse request body
	var req models.RegisterPhotoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if req.PhotoName == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "photo_name is required"})
		return
	}

	if req.Category != 0 && req.Category != 1 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "category must be 0 (surprise) or 1 (original)"})
		return
	}

	// Validate file extension
	ext := filepath.Ext(req.PhotoName)
	if !util.SupportedExt.Contains(ext) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: fmt.Sprintf("Unsupported file extension: %s. Supported: .jpeg, .jpg, .png", ext),
		})
		return
	}

	// Check if file exists in the original directory
	filePath := filepath.Join(ws.rootPath, "original", req.PhotoName)
	if req.Category == 0 {
		filePath = filepath.Join(ws.rootPath, "original/surprise", req.PhotoName)
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: fmt.Sprintf("Photo file does not exist: %s", req.PhotoName)})
		return
	}

	// Check for duplicates in database
	exists, err := ws.db.PhotoExists(req.PhotoName, req.Category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}
	if exists {
		c.JSON(http.StatusOK, models.RegisterPhotoResponse{
			PhotoName: req.PhotoName,
			Category:  req.Category,
			Order:     -1,
			Message:   fmt.Sprintf("Photo with name '%s' already exists in database", req.PhotoName),
		})
		return
	}

	// Get max order for the category
	maxOrder, err := ws.db.GetMaxOrder(req.Category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}

	// Insert into database
	if err := ws.db.InsertPhoto(req.PhotoName, req.Category, maxOrder); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to insert photo into database: %v", err)})
		return
	}

	c.Status(http.StatusCreated)
}

func (ws *WebServer) handleListPhotos(c *gin.Context) {
	// Parse query parameters
	categoryStr := c.DefaultQuery("category", "1")
	pageStr := c.DefaultQuery("page", "1")
	limitStr := c.DefaultQuery("limit", "20")

	category, err := strconv.Atoi(categoryStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid category parameter"})
		return
	}

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid page parameter"})
		return
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid limit parameter"})
		return
	}

	// Get total count
	total, err := ws.db.GetPhotoCount(category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}

	// Calculate offset
	offset := (page - 1) * limit

	// Get photos
	photos, err := ws.db.GetPhotos(category, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}

	c.JSON(http.StatusOK, models.PhotoListResponse{
		Photos: photos,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

func (ws *WebServer) handleDeletePhoto(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Photo name is required"})
		return
	}

	category := c.Param("category")
	if category == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Category is required"})
		return
	}

	categoryInt, err := strconv.Atoi(category)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid category parameter"})
		return
	}

	// Check if photo exists in database
	exists, err := ws.db.PhotoExists(name, categoryInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: fmt.Sprintf("Photo '%s' not found", name)})
		return
	}

	// Delete file from filesystem
	filePath := filepath.Join(ws.rootPath, "original", name)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to delete file: %v", err)})
		return
	}

	// Delete from database
	if err := ws.db.DeletePhoto(name, categoryInt); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to delete photo from database: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Photo '%s' deleted successfully", name)})
}

func (ws *WebServer) handleGetSettings(c *gin.Context) {
	settings, err := ws.db.GetAppSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to get settings: %v", err)})
		return
	}

	c.JSON(http.StatusOK, settings)
}

func (ws *WebServer) handleUpdateSettings(c *gin.Context) {
	var req store.AppSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if req.SlideshowIntervalSeconds <= 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "slideshow_interval_seconds must be positive"})
		return
	}

	newSettings := &req

	if err := ws.db.UpsertAppSettings(newSettings); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to update settings: %v", err)})
		return
	}

	// After updating settings, restart the slideshow with the new configuration.
	var imgPhotos []store.Photo
	if newSettings.IncludeSurprise {
		allPhotos, err := ws.getAllImages()
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to get photos for restart: %v", err)})
			return
		}
		imgPhotos = allPhotos
	} else {
		photos, err := ws.db.GetAllPhotos(1)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to get photos for restart: %v", err)})
			return
		}
		imgPhotos = photos
	}

	imgPaths := make([]string, len(imgPhotos))
	for i, p := range imgPhotos {
		imgPaths[i] = ws.buildImgPathFromPhoto(p)
	}

	if newSettings.ShuffleEnabled && len(imgPaths) > 1 {
		rand.Shuffle(len(imgPaths), func(i, j int) {
			imgPaths[i], imgPaths[j] = imgPaths[j], imgPaths[i]
		})
	}

	ws.imvMutex.Lock()
	defer ws.imvMutex.Unlock()
	if err := slideshow.RestartSlideshow(imgPaths, newSettings.SlideshowIntervalSeconds); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to restart slideshow: %v", err)})
		return
	}

	c.Status(http.StatusOK)
}

func (ws *WebServer) handleGetSchedule(c *gin.Context) {
	schedule, err := ws.db.GetSchedule()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to get settings: %v", err)})
		return
	}
	c.JSON(http.StatusOK, schedule)
}

var validScheduleTime = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)

func (ws *WebServer) handleUpdateSchedule(c *gin.Context) {
	var req store.Schedule
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if !validScheduleTime.MatchString(req.Start) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: fmt.Sprintf("Invalid start time format: need 23:15, got %s", req.Start)})
		return
	}

	if !validScheduleTime.MatchString(req.End) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: fmt.Sprintf("Invalid end time format: need 23:15, got %s", req.End)})
		return
	}

	newSchedule := &store.Schedule{
		Enabled: req.Enabled,
		Start:   req.Start,
		End:     req.End,
	}

	if err := ws.db.UpsertSchedule(newSchedule); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to update schedule: %v", err)})
		return
	}

	c.Status(http.StatusOK)
}

func (ws *WebServer) handlePlayFromPhoto(c *gin.Context) {
	photoName := c.Param("name")
	if photoName == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Photo name is required"})
		return
	}

	category := c.Param("category")
	if category == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Category is required"})
		return
	}

	photoCategory, err := strconv.Atoi(category)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: fmt.Sprintf("Category must be an integer, %v", err)})
		return
	}

	if photoCategory != 0 && photoCategory != 1 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "category must be 0 (surprise) or 1 (original)"})
		return
	}

	// Optional: validate extension similar to upload/register handlers
	ext := filepath.Ext(photoName)
	if ext == "" || !util.SupportedExt.Contains(ext) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: fmt.Sprintf("Unsupported or missing file extension: %s. Supported: .jpeg, .jpg, .png", ext),
		})
		return
	}

	// Ensure the photo exists in the database
	exists, err := ws.db.PhotoExists(photoName, photoCategory)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: fmt.Sprintf("Photo '%s' in category %d not found", photoName, photoCategory),
		})
		return
	}

	// Fetch all photos in the required order: category 0 then category 1
	allPhotos, err := ws.getAllImages()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to get image paths: %v", err)})
		return
	}
	if len(allPhotos) == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "No photos available to start slideshow"})
		return
	}

	imgPaths := make([]string, len(allPhotos))
	startIdx := -1

	for i, p := range allPhotos {
		imgPaths[i] = ws.buildImgPathFromPhoto(p)
		if p.PhotoName == photoName && p.Category == photoCategory {
			startIdx = i
		}
	}

	if startIdx == -1 {
		// Defensive: DB changed between existence check and fetch
		c.JSON(http.StatusNotFound, models.ErrorResponse{
			Error: fmt.Sprintf("Photo '%s' in category %d not found in current playlist", photoName, photoCategory),
		})
		return
	}

	// Rotate the slice so the requested photo is first
	var ordered []string
	if startIdx == 0 {
		ordered = imgPaths
	} else {
		ordered = append(imgPaths[startIdx:], imgPaths[:startIdx]...)
	}

	settings, err := ws.db.GetAppSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: fmt.Sprintf("Unable to fetch app settings, %v", err),
		})
		return
	}

	ws.imvMutex.Lock()
	defer ws.imvMutex.Unlock()
	// Let slideshow.RestartSlideshow handle defaulting when interval <= 0
	if err := slideshow.RestartSlideshow(ordered, settings.SlideshowIntervalSeconds); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error: fmt.Sprintf("Failed to restart slideshow: %v", err),
		})
		return
	}

	component := templates.PlayButtonIcons()
	component.Render(c.Request.Context(), c.Writer)
}

func (ws *WebServer) handleGetDisplay(c *gin.Context) {
	enabled, err := display.GetEnabled()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to get display state: %v", err)})
		return
	}

	c.JSON(http.StatusOK, models.DisplayStateResponse{Enabled: enabled})
}

func (ws *WebServer) handleUpdateDisplay(c *gin.Context) {
	state := c.Param("state")
	if state != "0" && state != "1" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "state must be 0 (off) or 1 (on)"})
		return
	}

	desiredEnabled := state == "1"
	if err := display.UpdateEnabled(desiredEnabled); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to update display state: %v", err)})
		return
	}

	// Re-read state to reflect actual output if possible.
	enabled, err := display.GetEnabled()
	if err != nil {
		slog.Warn("failed to re-read display state after update", "error", err)
		enabled = desiredEnabled
	}

	c.JSON(http.StatusOK, models.DisplayStateResponse{Enabled: enabled})
}

func (ws *WebServer) handlePhotoImage(c *gin.Context) {
	categoryStr := c.Param("category")
	encodedName := c.Param("name")

	if encodedName == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Photo name is required"})
		return
	}

	// Decode the photo name
	name, err := url.PathUnescape(encodedName)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid photo name encoding"})
		return
	}

	category, err := strconv.Atoi(categoryStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "Invalid category parameter"})
		return
	}

	// Determine file path based on category
	var filePath string
	if category == 0 {
		filePath = filepath.Join(ws.rootPath, "original/surprise", name)
	} else {
		filePath = filepath.Join(ws.rootPath, "original", name)
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: fmt.Sprintf("Photo file not found: %s", name)})
		return
	}

	// Serve the file
	c.File(filePath)
}

func (ws *WebServer) handleUIPhotos(c *gin.Context) {
	categoryStr := c.Param("category")
	category, err := strconv.Atoi(categoryStr)
	if err != nil {
		c.String(http.StatusBadRequest, "Invalid category")
		return
	}

	// Get all photos for this category
	photos, err := ws.db.GetAllPhotos(category)
	if err != nil {
		c.String(http.StatusInternalServerError, fmt.Sprintf("Error fetching photos: %v", err))
		return
	}

	component := templates.PhotoRow(photos, category)
	component.Render(c.Request.Context(), c.Writer)
}
