// Package models tracks all api models for request and responses
package models

import "github.com/aouyang1/digitalphotoframe/store"

type PhotoListResponse struct {
	Photos []store.Photo `json:"photos"`
	Total  int           `json:"total"`
	Page   int           `json:"page"`
	Limit  int           `json:"limit"`
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

type DisplayStateResponse struct {
	Enabled bool `json:"enabled"`
}
