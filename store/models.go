package store

type Photo struct {
	PhotoName string `json:"photo_name"`
	Category  int    `json:"category"`
	Order     int    `json:"order"`
}

type AppSettings struct {
	SlideshowIntervalSeconds int  `json:"slideshow_interval_seconds"`
	IncludeSurprise          bool `json:"include_surprise"`
	ShuffleEnabled           bool `json:"shuffle_enabled"`
}

type Schedule struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}
