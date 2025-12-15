package main

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type WebServer struct {
	router   *gin.Engine
	db       *Database
	rootPath string

	Updated chan bool
}

type UploadResponse struct {
	PhotoName string `json:"photo_name"`
	Category  int    `json:"category"`
	Order     int    `json:"order"`
	Message   string `json:"message"`
}

type PhotoListResponse struct {
	Photos []Photo `json:"photos"`
	Total  int     `json:"total"`
	Page   int     `json:"page"`
	Limit  int     `json:"limit"`
}

type ReorderRequest struct {
	NewOrder int `json:"new_order"`
}

type RegisterPhotoRequest struct {
	PhotoName string `json:"photo_name"`
	Category  int    `json:"category"`
}

type PlayFromPhotoRequest struct {
	PhotoName string `json:"photo_name"`
	Category  int    `json:"category"`
	Interval  int    `json:"interval"`
}

type SettingsResponse struct {
	SlideshowIntervalSeconds int  `json:"slideshow_interval_seconds"`
	IncludeSurprise          bool `json:"include_surprise"`
	ShuffleEnabled           bool `json:"shuffle_enabled"`
}

type UpdateSettingsRequest struct {
	SlideshowIntervalSeconds int  `json:"slideshow_interval_seconds"`
	IncludeSurprise          bool `json:"include_surprise"`
	ShuffleEnabled           bool `json:"shuffle_enabled"`
}

type RegisterPhotoResponse struct {
	PhotoName string `json:"photo_name"`
	Category  int    `json:"category"`
	Order     int    `json:"order"`
	Message   string `json:"message"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func NewWebServer(db *Database, rootPath string) *WebServer {
	router := gin.Default()

	ws := &WebServer{
		router:   router,
		db:       db,
		rootPath: rootPath,
		Updated:  make(chan bool),
	}

	// Setup routes
	ws.setupRoutes()

	return ws
}

func (ws *WebServer) setupRoutes() {
	// UI routes
	ws.router.GET("/", ws.handleMainUI)
	ws.router.GET("/ui/photos/:category", ws.handleUIPhotos)

	// API routes
	ws.router.POST("/upload", ws.handleUpload)
	ws.router.POST("/photos/register", ws.handleRegisterPhoto)
	ws.router.GET("/photos", ws.handleListPhotos)
	ws.router.GET("/photos/:category/:name/image", ws.handlePhotoImage)
	ws.router.DELETE("/photos/:name/category/:category", ws.handleDeletePhoto)
	ws.router.PUT("/photos/:name/reorder", ws.handleReorderPhoto)
	ws.router.POST("/slideshow/play", ws.handlePlayFromPhoto)
	ws.router.GET("/settings", ws.handleGetSettings)
	ws.router.PUT("/settings", ws.handleUpdateSettings)
}

func (ws *WebServer) Start(port string) {
	log.Printf("Starting web server on port %s", port)
	if err := ws.router.Run(port); err != nil {
		log.Fatalf("Failed to start web server: %v", err)
	}
}

func (ws *WebServer) getAllImages() ([]Photo, error) {
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

func (ws *WebServer) getImgPaths() ([]string, error) {
	allPhotos, err := ws.getAllImages()
	if err != nil {
		return nil, fmt.Errorf("failed to get all images: %v", err)
	}
	imgPaths := make([]string, len(allPhotos))
	for i, photo := range allPhotos {
		imgPaths[i] = ws.buildImgPathFromPhoto(photo)
	}
	slog.Info("image paths", "imgPaths", imgPaths)
	return imgPaths, nil
}

// buildImgPathFromPhoto constructs the filesystem path to the rotated (_IMGP) image
// corresponding to a Photo record, based on its category and the web server rootPath.
func (ws *WebServer) buildImgPathFromPhoto(photo Photo) string {
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
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "No file provided"})
		return
	}

	// Validate file extension
	ext := filepath.Ext(file.Filename)
	if !supportedExt.Contains(ext) {
		errorMsg := fmt.Sprintf("Unsupported file extension: %s. Supported: .jpeg, .jpg, .png", ext)
		if isHTMX {
			c.String(http.StatusBadRequest, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: errorMsg})
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
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errorMsg})
		return
	}
	if exists {
		errorMsg := fmt.Sprintf("Photo with name '%s' already exists", file.Filename)
		if isHTMX {
			c.String(http.StatusConflict, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusConflict, ErrorResponse{Error: errorMsg})
		return
	}

	// Ensure the original directory exists
	originalDir := filepath.Join(ws.rootPath, "original")
	if err := os.MkdirAll(originalDir, 0755); err != nil {
		errorMsg := fmt.Sprintf("Failed to create directory: %v", err)
		if isHTMX {
			c.String(http.StatusInternalServerError, "Error: "+errorMsg)
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errorMsg})
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
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errorMsg})
		return
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
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errorMsg})
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
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: errorMsg})
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

		// Generate HTML fragment
		html := ""
		for _, photo := range photos {
			encodedName := url.PathEscape(photo.PhotoName)
			imageURL := fmt.Sprintf("/photos/%d/%s/image", photo.Category, encodedName)
			html += fmt.Sprintf(
				"  <img src=\"%s\" alt=\"%s\" class=\"photo-thumbnail\" onclick=\"openPhotoModal('%s')\" />\n",
				imageURL,
				photo.PhotoName,
				imageURL,
			)
		}

		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))

		// trigger slideshow restart
		ws.Updated <- true
		return
	}

	c.JSON(http.StatusOK, UploadResponse{
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
	var req RegisterPhotoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if req.PhotoName == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "photo_name is required"})
		return
	}

	if req.Category != 0 && req.Category != 1 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "category must be 0 (surprise) or 1 (original)"})
		return
	}

	// Validate file extension
	ext := filepath.Ext(req.PhotoName)
	if !supportedExt.Contains(ext) {
		c.JSON(http.StatusBadRequest, ErrorResponse{
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
		c.JSON(http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Photo file does not exist: %s", req.PhotoName)})
		return
	}

	// Check for duplicates in database
	exists, err := ws.db.PhotoExists(req.PhotoName, req.Category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}
	if exists {
		c.JSON(http.StatusOK, RegisterPhotoResponse{
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
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}

	// Insert into database
	if err := ws.db.InsertPhoto(req.PhotoName, req.Category, maxOrder); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to insert photo into database: %v", err)})
		return
	}

	c.JSON(http.StatusOK, RegisterPhotoResponse{
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
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid category parameter"})
		return
	}

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid page parameter"})
		return
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit < 1 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid limit parameter"})
		return
	}

	// Get total count
	total, err := ws.db.GetPhotoCount(category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}

	// Calculate offset
	offset := (page - 1) * limit

	// Get photos
	photos, err := ws.db.GetPhotos(category, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}

	c.JSON(http.StatusOK, PhotoListResponse{
		Photos: photos,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

func (ws *WebServer) handleDeletePhoto(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Photo name is required"})
		return
	}

	category := c.Param("category")
	if category == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Category is required"})
		return
	}

	categoryInt, err := strconv.Atoi(category)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid category parameter"})
		return
	}

	// Check if photo exists in database
	exists, err := ws.db.PhotoExists(name, categoryInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Photo '%s' not found", name)})
		return
	}

	// Delete file from filesystem
	filePath := filepath.Join(ws.rootPath, "original", name)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to delete file: %v", err)})
		return
	}

	// Delete from database
	if err := ws.db.DeletePhoto(name, categoryInt); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to delete photo from database: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Photo '%s' deleted successfully", name)})
}

func (ws *WebServer) handleReorderPhoto(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Photo name is required"})
		return
	}

	category := c.Param("category")
	if category == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Category is required"})
		return
	}

	categoryInt, err := strconv.Atoi(category)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid category parameter"})
		return
	}

	// Parse request body
	var req ReorderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if req.NewOrder < 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "new_order must be non-negative"})
		return
	}

	// Get photo to determine category
	photo, err := ws.db.GetPhoto(name, categoryInt)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Photo '%s' not found", name)})
		return
	}

	// Get max order to validate new_order
	maxOrder, err := ws.db.GetMaxOrder(categoryInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}

	if req.NewOrder >= maxOrder {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: fmt.Sprintf("new_order %d exceeds maximum order %d for category %d", req.NewOrder, maxOrder-1, photo.Category),
		})
		return
	}

	// Update order
	if err := ws.db.UpdatePhotoOrder(name, req.NewOrder, categoryInt); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to update photo order: %v", err)})
		return
	}

	// Get updated photo
	updatedPhoto, err := ws.db.GetPhoto(name, categoryInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to retrieve updated photo: %v", err)})
		return
	}

	c.JSON(http.StatusOK, updatedPhoto)
}

func (ws *WebServer) handleGetSettings(c *gin.Context) {
	settings, err := ws.db.GetAppSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to get settings: %v", err)})
		return
	}

	resp := SettingsResponse{
		SlideshowIntervalSeconds: settings.SlideshowIntervalSeconds,
		IncludeSurprise:          settings.IncludeSurprise,
		ShuffleEnabled:           settings.ShuffleEnabled,
	}

	c.JSON(http.StatusOK, resp)
}

func (ws *WebServer) handleUpdateSettings(c *gin.Context) {
	var req UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if req.SlideshowIntervalSeconds <= 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "slideshow_interval_seconds must be positive"})
		return
	}

	newSettings := &AppSettings{
		SlideshowIntervalSeconds: req.SlideshowIntervalSeconds,
		IncludeSurprise:          req.IncludeSurprise,
		ShuffleEnabled:           req.ShuffleEnabled,
	}

	if err := ws.db.UpsertAppSettings(newSettings); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to update settings: %v", err)})
		return
	}

	resp := SettingsResponse{
		SlideshowIntervalSeconds: newSettings.SlideshowIntervalSeconds,
		IncludeSurprise:          newSettings.IncludeSurprise,
		ShuffleEnabled:           newSettings.ShuffleEnabled,
	}

	c.JSON(http.StatusOK, resp)
}

func (ws *WebServer) handlePlayFromPhoto(c *gin.Context) {
	var req PlayFromPhotoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: fmt.Sprintf("Invalid request body: %v", err)})
		return
	}

	if req.PhotoName == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "photo_name is required"})
		return
	}

	if req.Category != 0 && req.Category != 1 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "category must be 0 (surprise) or 1 (original)"})
		return
	}

	// Optional: validate extension similar to upload/register handlers
	ext := filepath.Ext(req.PhotoName)
	if ext == "" || !supportedExt.Contains(ext) {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: fmt.Sprintf("Unsupported or missing file extension: %s. Supported: .jpeg, .jpg, .png", ext),
		})
		return
	}

	// Ensure the photo exists in the database
	exists, err := ws.db.PhotoExists(req.PhotoName, req.Category)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Database error: %v", err)})
		return
	}
	if !exists {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: fmt.Sprintf("Photo '%s' in category %d not found", req.PhotoName, req.Category),
		})
		return
	}

	// Fetch all photos in the required order: category 0 then category 1
	allPhotos, err := ws.getAllImages()
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to get image paths: %v", err)})
		return
	}
	if len(allPhotos) == 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "No photos available to start slideshow"})
		return
	}

	imgPaths := make([]string, len(allPhotos))
	startIdx := -1

	for i, p := range allPhotos {
		imgPaths[i] = ws.buildImgPathFromPhoto(p)
		if p.PhotoName == req.PhotoName && p.Category == req.Category {
			startIdx = i
		}
	}

	if startIdx == -1 {
		// Defensive: DB changed between existence check and fetch
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: fmt.Sprintf("Photo '%s' in category %d not found in current playlist", req.PhotoName, req.Category),
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

	interval := req.Interval
	// Let restartSlideshow handle defaulting when interval <= 0
	if err := restartSlideshow(ordered, interval); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: fmt.Sprintf("Failed to restart slideshow: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "Slideshow restarted",
		"photo_name": req.PhotoName,
		"category":   req.Category,
		"interval":   interval,
	})
}

func (ws *WebServer) handlePhotoImage(c *gin.Context) {
	categoryStr := c.Param("category")
	encodedName := c.Param("name")

	if encodedName == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Photo name is required"})
		return
	}

	// Decode the photo name
	name, err := url.PathUnescape(encodedName)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid photo name encoding"})
		return
	}

	category, err := strconv.Atoi(categoryStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid category parameter"})
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
		c.JSON(http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Photo file not found: %s", name)})
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

	// Generate HTML fragment
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
				"onclick=\"event.stopPropagation(); playFromPhoto(this)\" "+
				"data-name=\"%s\" "+
				"data-category=\"%d\">"+
				"<i class=\"fa-solid fa-play\"></i>"+
				"</button>\n",
			encodedName,
			photo.Category,
		)

		// Only render delete button for "My Photos" (category 1)
		if category == 1 {
			deleteURL := fmt.Sprintf("/photos/%s/category/%d", encodedName, photo.Category)
			html += fmt.Sprintf(
				"    <button class=\"photo-delete-btn\" "+
					"title=\"Delete photo\" "+
					"onclick=\"event.stopPropagation(); if(!confirm('Delete this photo?')) { return false; }\" "+
					"hx-delete=\"%s\" "+
					"hx-target=\"this\" "+
					"hx-swap=\"none\" "+
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

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func (ws *WebServer) handleMainUI(c *gin.Context) {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Photo Gallery</title>
<script src="https://unpkg.com/htmx.org@1.9.10"></script>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css">
<style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
            background-color: #f5f5f5;
            padding: 20px;
        }
        
        .container {
            max-width: 1400px;
            margin: 0 auto;
            display: flex;
            min-height: calc(100vh - 40px);
        }
        
        .sidebar {
            width: 64px;
            background-color: #ffffff;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.08);
            display: flex;
            flex-direction: column;
            align-items: center;
            padding: 16px 0;
            margin-right: 20px;
            gap: 12px;
        }
        
        .nav-item {
            background: none;
            border: none;
            color: #888;
            cursor: pointer;
            width: 100%;
            padding: 10px 0;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 22px;
            transition: color 0.2s, background-color 0.2s;
        }
        
        .nav-item:hover {
            color: #333;
            background-color: rgba(0,0,0,0.03);
        }
        
        .nav-item.active {
            color: #007AFF;
            background-color: rgba(0,122,255,0.08);
        }
        
        .main-content {
            flex: 1;
            display: flex;
            flex-direction: column;
        }
        
        .view {
            display: none;
        }
        
        .view.active-view {
            display: block;
        }
        
        .category-section {
            margin-bottom: 40px;
        }
        
        .category-header {
            display: flex;
            align-items: center;
            gap: 15px;
            margin-bottom: 15px;
        }
        
        .category-title {
            font-size: 24px;
            font-weight: 600;
            color: #333;
            margin: 0;
        }
        
        .upload-form {
            display: inline-flex;
            align-items: center;
            gap: 8px;
        }
        
        .upload-button {
            background-color: #007AFF;
            color: white;
            border: none;
            padding: 8px 16px;
            border-radius: 6px;
            cursor: pointer;
            font-size: 14px;
            font-weight: 500;
            transition: background-color 0.2s;
        }
        
        .upload-button:hover {
            background-color: #0056CC;
        }
        
        .upload-button:disabled {
            background-color: #ccc;
            cursor: not-allowed;
        }
        
        .file-input {
            display: none;
        }
        
        .file-input-label {
            display: inline-block;
            padding: 8px 16px;
            background-color: #f0f0f0;
            border: 1px solid #ddd;
            border-radius: 6px;
            cursor: pointer;
            font-size: 14px;
            transition: background-color 0.2s;
        }
        
        .file-input-label:hover {
            background-color: #e0e0e0;
        }
        
        .upload-status {
            font-size: 14px;
            color: #666;
            margin-left: 10px;
        }
        
        .upload-status.success {
            color: #28a745;
        }
        
        .upload-status.error {
            color: #dc3545;
        }
        
        .file-name {
            font-size: 12px;
            color: #666;
            margin-left: 10px;
            font-style: italic;
        }
        
        .photo-row {
            display: flex;
            flex-wrap: wrap;
            gap: 10px;
            background-color: white;
            padding: 15px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }

        .photo-item {
            position: relative;
            display: inline-block;
        }
        
        .photo-thumbnail {
            width: 200px;
            height: 200px;
            object-fit: cover;
            border-radius: 4px;
            cursor: pointer;
            transition: transform 0.2s;
        }
        
        .photo-thumbnail:hover {
            transform: scale(1.05);
        }

        .photo-play-btn {
            position: absolute;
            bottom: 8px;
            left: 50%;
            transform: translateX(-50%);
            background-color: rgba(0, 0, 0, 0.4);
            color: #fff;
            border: none;
            border-radius: 50%;
            width: 32px;
            height: 32px;
            display: flex;
            align-items: center;
            justify-content: center;
            cursor: pointer;
            font-size: 18px;
            text-shadow: 0 1px 3px rgba(0,0,0,0.8);
			padding-left: 3px;
        }

        .photo-delete-btn {
            position: absolute;
            bottom: 8px;
            right: 8px;
            background-color: transparent;
            color: #fff;
            border: none;
            border-radius: 50%;
            width: 32px;
            height: 32px;
            display: flex;
            align-items: center;
            justify-content: center;
            cursor: pointer;
            font-size: 18px;
            text-shadow: 0 1px 3px rgba(0,0,0,0.8);
            transition: transform 0.1s;
        }

        .photo-delete-btn:hover {
            transform: scale(1.05);
        }
        
        .loading {
            color: #666;
            font-style: italic;
        }
        
        .photo-modal {
            display: none;
            position: fixed;
            z-index: 1000;
            left: 0;
            top: 0;
            width: 100%;
            height: 100%;
            background-color: rgba(0, 0, 0, 0.9);
            cursor: pointer;
        }
        
        .photo-modal.active {
            display: flex;
            align-items: center;
            justify-content: center;
        }
        
        .photo-modal-content {
            max-width: 90%;
            max-height: 90%;
            object-fit: contain;
            border-radius: 4px;
        }
        
        .photo-modal-close {
            position: absolute;
            top: 20px;
            right: 30px;
            color: #fff;
            font-size: 40px;
            font-weight: bold;
            cursor: pointer;
            z-index: 1001;
        }
        
        .photo-modal-close:hover {
            color: #ccc;
        }
    </style>
</head>
<body>
    <div class="container">
        <nav class="sidebar">
            <button class="nav-item active" type="button" data-view="photos" onclick="switchView('photos', this)">
                <i class="fa-solid fa-images"></i>
            </button>
            <button class="nav-item" type="button" data-view="slideshow" onclick="switchView('slideshow', this)">
                <i class="fa-solid fa-play-circle"></i>
            </button>
            <button class="nav-item" type="button" data-view="settings" onclick="switchView('settings', this)">
                <i class="fa-solid fa-gear"></i>
            </button>
        </nav>
        <div class="main-content">
            <div id="view-photos" class="view active-view">
                <div class="category-section">
                    <div class="category-header">
                    	<h2 class="category-title">Surprise</h2>
					</div>
                    <div id="surprise-photos" class="photo-row loading" 
                     hx-get="/ui/photos/0" 
                     hx-trigger="load" 
                     hx-swap="innerHTML">
                     Loading...
                    </div>
                </div>
                
                <div class="category-section">
                    <div class="category-header">
                        <h2 class="category-title">My Photos</h2>
                        <form class="upload-form" 
                              hx-post="/upload" 
                              hx-encoding="multipart/form-data"
                              hx-target="#my-photos"
                              hx-swap="innerHTML"
                              hx-indicator="#upload-indicator"
                              id="upload-form">
                            <label for="file-input" class="file-input-label">Upload</label>
                            <input type="file" 
                                   id="file-input" 
                                   name="file" 
                                   class="file-input" 
                                   accept=".jpg,.jpeg,.png,.JPG,.JPEG,.PNG"
                                   required
                                   onchange="document.getElementById('file-name').textContent=this.files[0]?this.files[0].name:''; htmx.trigger('#upload-form', 'submit');">
                            <span id="file-name" class="file-name"></span>
                            <span id="upload-indicator" class="upload-status" style="display: none;">Uploading...</span>
                        </form>
                    </div>
                    <div id="my-photos" class="photo-row loading" 
                         hx-get="/ui/photos/1" 
                         hx-trigger="load, refreshPhotos from:body" 
                         hx-swap="innerHTML">
                        Loading...
                    </div>
                </div>
            </div>

            <div id="view-slideshow" class="view">
                <div class="category-section">
                    <h2 class="category-title">Slideshow</h2>
                    <p class="upload-status">Slideshow controls coming soon.</p>
                </div>
            </div>

            <div id="view-settings" class="view">
                <div class="category-section">
                    <h2 class="category-title">Settings</h2>
                    <p class="upload-status">Settings will appear here.</p>
                </div>
            </div>
        </div>
    </div>
    
    <div id="photo-modal" class="photo-modal" onclick="closePhotoModal()">
        <span class="photo-modal-close" onclick="event.stopPropagation(); closePhotoModal()">&times;</span>
        <img id="photo-modal-img" class="photo-modal-content" src="" alt="Full size photo" onclick="event.stopPropagation();">
    </div>
    
    <script>
        function openPhotoModal(imageUrl) {
            const modal = document.getElementById('photo-modal');
            const modalImg = document.getElementById('photo-modal-img');
            modalImg.src = imageUrl;
            modal.classList.add('active');
            document.body.style.overflow = 'hidden';
        }
        
        function closePhotoModal() {
            const modal = document.getElementById('photo-modal');
            modal.classList.remove('active');
            document.body.style.overflow = 'auto';
        }
        
        // Close modal on Escape key
        document.addEventListener('keydown', function(event) {
            if (event.key === 'Escape') {
                closePhotoModal();
            }
        });

        function switchView(viewName, button) {
            const views = document.querySelectorAll('.view');
            views.forEach(function(view) {
                view.classList.remove('active-view');
            });

            const target = document.getElementById('view-' + viewName);
            if (target) {
                target.classList.add('active-view');
            }

            const navItems = document.querySelectorAll('.nav-item');
            navItems.forEach(function(item) {
                item.classList.remove('active');
            });

            if (button) {
                button.classList.add('active');
            }
        }

        function playFromPhoto(button) {
            const encodedName = button.dataset.name;
            const category = parseInt(button.dataset.category, 10);
            if (!encodedName || Number.isNaN(category)) {
                console.error('Missing photo metadata for playFromPhoto');
                return;
            }
            const photoName = decodeURIComponent(encodedName);

            fetch('/slideshow/play', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({
                    photo_name: photoName,
                    category: category,
                    interval: 0
                })
            }).then(response => {
                if (!response.ok) {
                    return response.json().then(data => {
                        const msg = data && data.error ? data.error : 'Failed to start slideshow';
                        alert(msg);
                    }).catch(() => {
                        alert('Failed to start slideshow');
                    });
                }
            }).catch(() => {
                alert('Failed to start slideshow');
            });
        }
    </script>
</body>
</html>`

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}
