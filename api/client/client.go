package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aouyang1/digitalphotoframe/api/models"
	"github.com/aouyang1/digitalphotoframe/store"
)

type PhotoClient struct {
	baseURL string
	client  *http.Client
}

func NewPhotoClient(baseURL string) *PhotoClient {
	return &PhotoClient{
		baseURL: baseURL,
		client:  &http.Client{},
	}
}

// RegisterPhoto registers an existing photo file in the database
func (pc *PhotoClient) RegisterPhoto(photoPath string, category int) error {
	photoName := filepath.Base(photoPath)

	// Check if file exists
	if _, err := os.Stat(photoPath); os.IsNotExist(err) {
		return fmt.Errorf("photo file does not exist: %s", photoPath)
	}

	reqBody := models.RegisterPhotoRequest{
		PhotoName: photoName,
		Category:  category,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/photos/register", pc.baseURL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := pc.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp models.ErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			return fmt.Errorf("server error: %s", errResp.Error)
		}
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	var registerResp models.RegisterPhotoResponse
	if err := json.Unmarshal(body, &registerResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	slog.Info("photo registered successfully", "name", photoName, "category", category, "order", registerResp.Order)
	return nil
}

// RegisterPhotoIfNotExists registers a photo only if it doesn't already exist
func (pc *PhotoClient) RegisterPhotoIfNotExists(photoPath string, category int) error {
	err := pc.RegisterPhoto(photoPath, category)
	if err != nil {
		// Check if error is due to duplicate
		if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "409") {
			slog.Debug("photo already registered, skipping", "path", photoPath)
			return nil
		}
		return err
	}
	return nil
}

// GetPhotos retrieves all photos for a given category from the database
func (pc *PhotoClient) GetPhotos(category int) ([]store.Photo, error) {
	// Fetch all photos by using a large limit and paginating if needed
	var allPhotos []store.Photo
	page := 1
	limit := 100

	for {
		url := fmt.Sprintf("%s/photos?category=%d&page=%d&limit=%d", pc.baseURL, category, page, limit)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := pc.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			var errResp models.ErrorResponse
			if err := json.Unmarshal(body, &errResp); err == nil {
				return nil, fmt.Errorf("server error: %s", errResp.Error)
			}
			return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
		}

		var listResp models.PhotoListResponse
		if err := json.Unmarshal(body, &listResp); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		allPhotos = append(allPhotos, listResp.Photos...)

		// Check if we've fetched all photos
		if len(listResp.Photos) < limit || len(allPhotos) >= listResp.Total {
			break
		}

		page++
	}

	return allPhotos, nil
}

// DeletePhoto deletes a photo from the database (and filesystem if it exists)
func (pc *PhotoClient) DeletePhoto(name string, category int) error {
	encodedName := url.PathEscape(name)
	deleteURL := fmt.Sprintf("%s/photos/%s/category/%d", pc.baseURL, encodedName, category)
	req, err := http.NewRequest("DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := pc.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		var errResp models.ErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			return fmt.Errorf("server error: %s", errResp.Error)
		}
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
