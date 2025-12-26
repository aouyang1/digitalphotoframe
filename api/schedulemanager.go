package api

import (
	"errors"
	"log/slog"
	"time"

	"github.com/aouyang1/digitalphotoframe/display"
	"github.com/aouyang1/digitalphotoframe/store"
)

const scheduleInterval = time.Minute

// ScheduleManager will periodically check the time to decide if we need to turn off or on the display
type ScheduleManager struct {
	db *store.Database

	lastCheck time.Time
}

func NewScheduleManager(db *store.Database) (*ScheduleManager, error) {
	if db == nil {
		return nil, errors.New("no database provided for scheduler")
	}

	return &ScheduleManager{
		db: db,
	}, nil
}

func (s *ScheduleManager) checkSchedule() {
	schedule, err := s.db.GetSchedule()
	if err != nil {
		slog.Error("unable to get schedule", "error", err)
		return
	}

	if !schedule.Enabled {
		return
	}

	now := time.Now()
	defer func() { s.lastCheck = now }()

	startTime, err := time.Parse("15:04", schedule.Start)
	if err != nil {
		slog.Warn("start time with invalid format", "start", schedule.Start, "error", err)
		return
	}
	startDate := time.Date(now.Year(), now.Month(), now.Day(), startTime.Hour(), startTime.Minute(), 0, 0, now.Location())

	endTime, err := time.Parse("15:04", schedule.End)
	if err != nil {
		slog.Warn("end time with invalid format", "start", schedule.End, "error", err)
		return
	}

	endDate := time.Date(now.Year(), now.Month(), now.Day(), endTime.Hour(), endTime.Minute(), 0, 0, now.Location())
	if startTime.After(endTime) {
		endDate = endDate.Add(24 * time.Hour)
	}

	// crossed into end of schedule - turn off display
	if s.lastCheck.Before(endDate) && now.After(endDate) {
		if err := display.UpdateEnabled(false); err != nil {
			slog.Warn("issue while turning off display for schedule", "error", err)
		} else {
			slog.Info("turning display off for schedule", "time", now)
		}
		return
	}

	// crossed into start of schedule - turn on display
	if now.After(startDate) && s.lastCheck.Before(startDate) {
		if err := display.UpdateEnabled(true); err != nil {
			slog.Warn("issue while turning on display for schedule", "error", err)
		} else {
			slog.Info("turning display on for schedule", "time", now)
		}
		return
	}
}

func (s *ScheduleManager) Run() {
	ticker := time.NewTicker(scheduleInterval)

	s.checkSchedule()

	// Initial sync
	for range ticker.C {
		s.checkSchedule()
	}
}
