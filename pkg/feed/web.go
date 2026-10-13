package feed

import (
	"fmt"
	"time"

	"github.com/mxpv/podsync/pkg/model"
)

// WebDefaults contains only the feed options available to web-created shows.
// Executable hooks, downloader arguments and filesystem paths stay host-only.
type WebDefaults struct {
	PageSize     int           `toml:"page_size"`
	UpdatePeriod time.Duration `toml:"update_period"`
	Quality      model.Quality `toml:"quality"`
	Format       model.Format  `toml:"format"`
	PlaylistSort model.Sorting `toml:"playlist_sort"`
	Clean        *Cleanup      `toml:"clean"`
	Filters      Filters       `toml:"filters"`
}

func (d *WebDefaults) ApplyDefaults() {
	if d.PageSize == 0 {
		d.PageSize = 10
	}
	if d.UpdatePeriod == 0 {
		d.UpdatePeriod = model.DefaultUpdatePeriod
	}
	if d.Quality == "" {
		d.Quality = model.QualityHigh
	}
	if d.Format == "" {
		d.Format = model.FormatAudio
	}
	if d.PlaylistSort == "" {
		d.PlaylistSort = model.SortingAsc
	}
	if d.Clean == nil {
		d.Clean = &Cleanup{KeepLast: 50}
	}
}

func (d WebDefaults) Validate() error {
	if d.PageSize < 1 || d.PageSize > 100 || d.UpdatePeriod < time.Hour ||
		d.Clean == nil || d.Clean.KeepLast < 1 || d.Clean.KeepLast > 1000 {
		return fmt.Errorf("web feed defaults require page_size 1-100, update_period >= 1h and clean.keep_last 1-1000")
	}
	if (d.Format != model.FormatAudio && d.Format != model.FormatVideo) ||
		(d.Quality != model.QualityHigh && d.Quality != model.QualityLow) ||
		(d.PlaylistSort != model.SortingAsc && d.PlaylistSort != model.SortingDesc) {
		return fmt.Errorf("invalid web feed format, quality or playlist_sort")
	}
	return nil
}

func (d WebDefaults) Feed(id, link string) *Config {
	cleanup := *d.Clean
	return &Config{
		ID: id, URL: link, PageSize: d.PageSize, UpdatePeriod: d.UpdatePeriod,
		Quality: d.Quality, Format: d.Format, PlaylistSort: d.PlaylistSort,
		Clean: &cleanup, Filters: d.Filters, OPML: true,
		Custom:        Custom{CoverArtQuality: d.Quality},
		YouTubeDLArgs: []string{"--match-filter", "!is_live"},
	}
}
