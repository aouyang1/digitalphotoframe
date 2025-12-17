// Package models tracks all api models for request and responses
package models

import "github.com/aouyang1/digitalphotoframe/store"

type UploadResponse struct {
	PhotoName string `json:"photo_name"`
	Category  int    `json:"category"`
	Order     int    `json:"order"`
	Message   string `json:"message"`
}

type PhotoListResponse struct {
	Photos []store.Photo `json:"photos"`
	Total  int           `json:"total"`
	Page   int           `json:"page"`
	Limit  int           `json:"limit"`
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

type DisplayStateResponse struct {
	Enabled bool `json:"enabled"`
}
