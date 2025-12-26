// Package api is the main api web server
package api

import (
	"embed"
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
	"github.com/aouyang1/digitalphotoframe/slideshow"
	"github.com/aouyang1/digitalphotoframe/store"
	"github.com/aouyang1/digitalphotoframe/util"
	"github.com/aouyang1/digitalphotoframe/wlrrandr"
	"github.com/gin-gonic/gin"
)

//go:embed web/templates/* web/static/**
var webFiles embed.FS

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
	ws.router.PUT("/photos/:name/reorder", ws.handleReorderPhoto)
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

	// Get the file from the form
	file, err := c.FormFile("file")
	if err != nil {
		if isHTMX {
			c.String(http.StatusBadRequest, "Error: No file provided")
			return
		}
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "No file provided"})
		return
	}

	// Validate file extension
	ext := filepath.Ext(file.Filename)
	if !util.SupportedExt.Contains(ext) {
		errorMsg := fmt.Sprintf("Unsupported file extension: %s. Supported: .jpeg, .jpg, .png", ext)
		if isHTMX {
			c.String(http.StatusBadRequest, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: errorMsg})
		return
	}

	// Check for duplicates
	exists, err := ws.db.PhotoExists(file.Filename, 1)
	if err != nil {
		errorMsg := fmt.Sprintf("Database error: %v", err)
		if isHTMX {
			c.String(http.StatusInternalServerError, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: errorMsg})
		return
	}
	if exists {
		errorMsg := fmt.Sprintf("Photo with name '%s' already exists", file.Filename)
		if isHTMX {
			c.String(http.StatusConflict, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusConflict, models.ErrorResponse{Error: errorMsg})
		return
	}

	// Ensure the original directory exists
	originalDir := filepath.Join(ws.rootPath, "original")
	if err := os.MkdirAll(originalDir, 0o755); err != nil {
		errorMsg := fmt.Sprintf("Failed to create directory: %v", err)
		if isHTMX {
			c.String(http.StatusInternalServerError, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: errorMsg})
		return
	}

	// Save file to disk
	filePath := filepath.Join(originalDir, file.Filename)
	if err := c.SaveUploadedFile(file, filePath); err != nil {
		errorMsg := fmt.Sprintf("Failed to save file: %v", err)
		if isHTMX {
			c.String(http.StatusInternalServerError, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: errorMsg})
		return
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
		os.Remove(filePath)
		errorMsg := fmt.Sprintf("Database error: %v", err)
		if isHTMX {
			c.String(http.StatusInternalServerError, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: errorMsg})
		return
	}

	// Insert into database
	if err := ws.db.InsertPhoto(file.Filename, 1, maxOrder); err != nil {
		// Clean up file if DB insert fails
		os.Remove(filePath)
		errorMsg := fmt.Sprintf("Failed to insert photo into database: %v", err)
		if isHTMX {
			c.String(http.StatusInternalServerError, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: errorMsg})
		return
	}

	// If HTMX request, return HTML fragment with updated photos
	if isHTMX {
		// Get all photos for category 1
		photos, err := ws.db.GetAllPhotos(1)
		if err != nil {
			c.String(http.StatusInternalServerError, "Error: Failed to refresh photos")
			return
		}

		html := ws.generateUIPhotosHTML(photos, 1)
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))

		// trigger slideshow restart
		ws.Updated <- true
		return
	}

	c.JSON(http.StatusOK, models.UploadResponse{
		PhotoName: file.Filename,
		Category:  1,
		Order:     maxOrder,
		Message:   "Photo uploaded successfully",
	})

	// trigger slideshow restart
	ws.Updated <- true
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

	c.JSON(http.StatusOK, models.RegisterPhotoResponse{
		PhotoName: req.PhotoName,
		Category:  req.Category,
		Order:     maxOrder,
		Message:   "Photo registered successfully",
	})
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

func (ws *WebServer) handleReorderPhoto(c *gin.Context) {
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

	// Parse request body
	var req models.ReorderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if req.NewOrder < 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{Error: "new_order must be non-negative"})
		return
	}

	// Get photo to determine category
	photo, err := ws.db.GetPhoto(name, categoryInt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse{Error: fmt.Sprintf("Photo '%s' not found", name)})
		return
	}

	// Get max order to validate new_order
	maxOrder, err := ws.db.GetMaxOrder(categoryInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}

	if req.NewOrder >= maxOrder {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error: fmt.Sprintf("new_order %d exceeds maximum order %d for category %d", req.NewOrder, maxOrder-1, photo.Category),
		})
		return
	}

	// Update order
	if err := ws.db.UpdatePhotoOrder(name, req.NewOrder, categoryInt); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to update photo order: %v", err)})
		return
	}

	// Get updated photo
	updatedPhoto, err := ws.db.GetPhoto(name, categoryInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to retrieve updated photo: %v", err)})
		return
	}

	c.JSON(http.StatusOK, updatedPhoto)
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

	c.JSON(http.StatusOK, newSettings)
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

	c.JSON(http.StatusOK, newSchedule)
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

	c.JSON(http.StatusOK, gin.H{
		"message":    "Slideshow restarted",
		"photo_name": photoName,
		"category":   photoCategory,
		"interval":   settings.SlideshowIntervalSeconds,
	})
}

func (ws *WebServer) handleGetDisplay(c *gin.Context) {
	enabled, err := wlrrandr.GetDisplayEnabled()
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
	if err := wlrrandr.UpdateDisplayEnabled(desiredEnabled); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{Error: fmt.Sprintf("Failed to update display state: %v", err)})
		return
	}

	// Re-read state to reflect actual output if possible.
	enabled, err := wlrrandr.GetDisplayEnabled()
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

	html := ws.generateUIPhotosHTML(photos, category)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func (ws *WebServer) generateUIPhotosHTML(photos []store.Photo, category int) string {
	html := "<div class=\"photo-row\">\n"
	for _, photo := range photos {
		encodedName := url.PathEscape(photo.PhotoName)
		imageURL := fmt.Sprintf("/photos/%d/%s/image", photo.Category, encodedName)

		// Start photo container
		html += "  <div class=\"photo-item\">\n"

		// Photo thumbnail
		html += fmt.Sprintf(
			"    <img src=\"%s\" alt=\"%s\" class=\"photo-thumbnail\" onclick=\"openPhotoModal('%s')\" />\n",
			imageURL,
			photo.PhotoName,
			imageURL,
		)

		// Play button for both categories
		html += fmt.Sprintf(
			"    <button class=\"photo-play-btn\" "+
				"title=\"Play slideshow from this photo\" "+
				"hx-post=\"/slideshow/play/%s/category/%d\" "+
				"hx-on:click=\"event.stopPropagation()\" "+
				"hx-trigger=\"click\" "+
				"hx-indicator=\"#play-indicator\" >"+
				"<i class=\"fa-solid fa-play\"></i>"+
				"</button>\n"+
				"<span id=\"play-indicator\" class=\"upload-status\" style=\"display: none;\">Starting Slideshow...</span>\n",
			encodedName,
			photo.Category,
		)

		// Only render delete button for "My Photos" (category 1)
		if category == 1 {
			deleteURL := fmt.Sprintf("/photos/%s/category/%d", encodedName, photo.Category)
			html += fmt.Sprintf(
				"    <button class=\"photo-delete-btn\" "+
					"title=\"Delete photo\" "+
					"hx-delete=\"%s\" "+
					"hx-target=\"this\" "+
					"hx-swap=\"none\" "+
					"hx-confirm=\"Delete this photo?\" "+
					"hx-on::after-request=\"if(event.detail.xhr.status===200){ htmx.trigger(document.body, 'refreshPhotos') }\">"+
					"<i class=\"fa-solid fa-trash-can\"></i>"+
					"</button>\n",
				deleteURL,
			)
		}

		// End photo container
		html += "  </div>\n"
	}
	html += "</div>"
	return html
}
