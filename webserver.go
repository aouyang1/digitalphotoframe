package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
)

type WebServer struct {
	router   *gin.Engine
	db       *Database
	rootPath string
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
}

func (ws *WebServer) Start(port string) {
	log.Printf("Starting web server on port %s", port)
	if err := ws.router.Run(port); err != nil {
		log.Fatalf("Failed to start web server: %v", err)
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
				"  <img src=\"%s\" alt=\"%s\" class=\"photo-thumbnail\" />\n",
				imageURL,
				photo.PhotoName,
			)
		}

		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
		return
	}

	c.JSON(http.StatusOK, UploadResponse{
		PhotoName: file.Filename,
		Category:  1,
		Order:     maxOrder,
		Message:   "Photo uploaded successfully",
	})
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
	photo, err := ws.db.GetPhoto(name)
	if err != nil {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Photo '%s' not found", name)})
		return
	}

	// Get max order to validate new_order
	maxOrder, err := ws.db.GetMaxOrder(photo.Category)
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
	if err := ws.db.UpdatePhotoOrder(name, req.NewOrder, photo.Category); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to update photo order: %v", err)})
		return
	}

	// Get updated photo
	updatedPhoto, err := ws.db.GetPhoto(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Failed to retrieve updated photo: %v", err)})
		return
	}

	c.JSON(http.StatusOK, updatedPhoto)
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
		html += fmt.Sprintf(
			"  <img src=\"%s\" alt=\"%s\" class=\"photo-thumbnail\" />\n",
			imageURL,
			photo.PhotoName,
		)
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
        }
        
        .category-section {
            margin-bottom: 40px;
        }
        
        .category-header {
            display: flex;
            align-items: center;
            justify-content: space-between;
            margin-bottom: 15px;
        }
        
        .category-title {
            font-size: 24px;
            font-weight: 600;
            color: #333;
        }
        
        .upload-form {
            display: inline-flex;
            align-items: center;
            gap: 10px;
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
        
        .loading {
            color: #666;
            font-style: italic;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="category-section">
            <h2 class="category-title">Surprise</h2>
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
                    <label for="file-input" class="file-input-label">Upload Photo</label>
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
</body>
</html>`

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}
