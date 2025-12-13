package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Database struct {
	db *sql.DB
}

type Photo struct {
	PhotoName string `json:"photo_name"`
	Category  int    `json:"category"`
	Order     int    `json:"order"`
}

func NewDatabase(dbPath string) (*Database, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	database := &Database{db: db}

	// Create table if it doesn't exist
	if err := database.createTable(); err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	return database, nil
}

func (d *Database) createTable() error {
	query := `
	CREATE TABLE IF NOT EXISTS photos (
		photo_name TEXT NOT NULL,
		category INTEGER NOT NULL,
		"order" INTEGER NOT NULL,
		PRIMARY KEY (photo_name, category)
	);
	CREATE INDEX IF NOT EXISTS idx_photos_category_order ON photos(category, "order");
	`
	_, err := d.db.Exec(query)
	return err
}

func (d *Database) InsertPhoto(name string, category int, order int) error {
	query := `INSERT INTO photos (photo_name, category, "order") VALUES (?, ?, ?)`
	_, err := d.db.Exec(query, name, category, order)
	if err != nil {
		return fmt.Errorf("failed to insert photo: %w", err)
	}
	return nil
}

func (d *Database) GetPhotos(category int, limit int, offset int) ([]Photo, error) {
	query := `
		SELECT photo_name, category, "order"
		FROM photos
		WHERE category = ?
		ORDER BY "order" ASC
		LIMIT ? OFFSET ?
	`
	rows, err := d.db.Query(query, category, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query photos: %w", err)
	}
	defer rows.Close()

	var photos []Photo
	for rows.Next() {
		var p Photo
		if err := rows.Scan(&p.PhotoName, &p.Category, &p.Order); err != nil {
			return nil, fmt.Errorf("failed to scan photo: %w", err)
		}
		photos = append(photos, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return photos, nil
}

func (d *Database) GetAllPhotos(category int) ([]Photo, error) {
	query := `
		SELECT photo_name, category, "order"
		FROM photos
		WHERE category = ?
		ORDER BY "order" DESC
	`
	rows, err := d.db.Query(query, category)
	if err != nil {
		return nil, fmt.Errorf("failed to query photos: %w", err)
	}
	defer rows.Close()

	var photos []Photo
	for rows.Next() {
		var p Photo
		if err := rows.Scan(&p.PhotoName, &p.Category, &p.Order); err != nil {
			return nil, fmt.Errorf("failed to scan photo: %w", err)
		}
		photos = append(photos, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return photos, nil
}

func (d *Database) GetPhotoCount(category int) (int, error) {
	query := `SELECT COUNT(*) FROM photos WHERE category = ?`
	var count int
	err := d.db.QueryRow(query, category).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get photo count: %w", err)
	}
	return count, nil
}

func (d *Database) DeletePhoto(name string, category int) error {
	query := `DELETE FROM photos WHERE photo_name = ? AND category = ?`
	result, err := d.db.Exec(query, name, category)
	if err != nil {
		return fmt.Errorf("failed to delete photo: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("photo not found: %s in category %d", name, category)
	}

	return nil
}

func (d *Database) GetMaxOrder(category int) (int, error) {
	query := `SELECT COALESCE(MAX("order"), -1) FROM photos WHERE category = ?`
	var maxOrder int
	err := d.db.QueryRow(query, category).Scan(&maxOrder)
	if err != nil {
		return 0, fmt.Errorf("failed to get max order: %w", err)
	}
	return maxOrder + 1, nil
}

func (d *Database) PhotoExists(name string, category int) (bool, error) {
	query := `SELECT COUNT(*) FROM photos WHERE photo_name = ? AND category = ?`
	var count int
	err := d.db.QueryRow(query, name, category).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check photo existence: %w", err)
	}
	return count > 0, nil
}

func (d *Database) GetPhoto(name string) (*Photo, error) {
	query := `SELECT photo_name, category, "order" FROM photos WHERE photo_name = ?`
	var p Photo
	err := d.db.QueryRow(query, name).Scan(&p.PhotoName, &p.Category, &p.Order)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("photo not found: %s", name)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get photo: %w", err)
	}
	return &p, nil
}

func (d *Database) UpdatePhotoOrder(name string, newOrder int, category int) error {
	// Get current order of the photo
	photo, err := d.GetPhoto(name)
	if err != nil {
		return err
	}

	if photo.Category != category {
		return fmt.Errorf("photo category mismatch")
	}

	oldOrder := photo.Order

	// If order hasn't changed, no need to update
	if oldOrder == newOrder {
		return nil
	}

	// Use transaction to ensure atomicity
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if oldOrder < newOrder {
		// Moving down: decrement orders [oldOrder+1, newOrder] by 1
		query := `
			UPDATE photos
			SET "order" = "order" - 1
			WHERE category = ? AND "order" > ? AND "order" <= ?
		`
		_, err = tx.Exec(query, category, oldOrder, newOrder)
		if err != nil {
			return fmt.Errorf("failed to shift orders down: %w", err)
		}
	} else {
		// Moving up: increment orders [newOrder, oldOrder-1] by 1
		query := `
			UPDATE photos
			SET "order" = "order" + 1
			WHERE category = ? AND "order" >= ? AND "order" < ?
		`
		_, err = tx.Exec(query, category, newOrder, oldOrder)
		if err != nil {
			return fmt.Errorf("failed to shift orders up: %w", err)
		}
	}

	// Update the photo's order
	query := `UPDATE photos SET "order" = ? WHERE photo_name = ?`
	_, err = tx.Exec(query, newOrder, name)
	if err != nil {
		return fmt.Errorf("failed to update photo order: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	slog.Info("photo order updated", "name", name, "old_order", oldOrder, "new_order", newOrder)
	return nil
}

func (d *Database) Close() error {
	return d.db.Close()
}
