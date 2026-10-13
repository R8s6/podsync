package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mxpv/podsync/pkg/db"
	"github.com/mxpv/podsync/pkg/feed"
)

func TestWebAdminConfigAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	base := fmt.Sprintf(`[server]
web_ui = true
hostname = "https://podsync.test/podcasts"
[storage.local]
data_dir = %q
[web_admin]
enabled = true
token = %q
[web_admin.defaults]
page_size = 10
update_period = "60h"
format = "audio"
quality = "high"
clean = { keep_last = 500 }
filters = { min_duration = 600 }
`, filepath.Join(dir, "media"), strings.Repeat("a", 64))
	require.NoError(t, os.WriteFile(path, []byte(base), 0600))
	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "https://podsync.test", cfg.WebAdmin.Origin)
	store, err := db.NewBadger(&cfg.Database)
	require.NoError(t, err)
	require.NoError(t, cfg.loadManagedFeeds(store)) // First run may have no feeds.
	f := cfg.WebAdmin.Defaults.Feed("web_example", "https://www.youtube.com/@example")
	require.NoError(t, store.AddManagedFeed(f))
	require.NoError(t, store.Close())

	// Definitions remain active with management disabled and without an admin token.
	disabled := strings.ReplaceAll(strings.ReplaceAll(base, "enabled = true", "enabled = false"), strings.Repeat("a", 64), "")
	require.NoError(t, os.WriteFile(path, []byte(disabled), 0600))
	cfg, err = LoadConfig(path)
	require.NoError(t, err)
	store, err = db.NewBadger(&cfg.Database)
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, cfg.loadManagedFeeds(store))
	require.Equal(t, f, cfg.Feeds[f.ID])
	// Host-defined configuration is never overwritten by a saved definition.
	cfg.Feeds[f.ID] = &feed.Config{ID: f.ID, URL: "host-defined"}
	require.NoError(t, cfg.loadManagedFeeds(store))
	require.Equal(t, "host-defined", cfg.Feeds[f.ID].URL)

	for _, replacements := range [][2]string{
		{strings.Repeat("a", 64), "short"}, {"https://podsync.test", "http://podsync.test"},
		{"web_ui = true", "web_ui = false"}, {"update_period = \"60h\"", "update_period = \"1s\""},
		{"keep_last = 500", "keep_last = 0"}, {"page_size = 10", "page_size = 100000"},
		{filepath.Join(dir, "media"), dir},
	} {
		require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(base, replacements[0], replacements[1])), 0600))
		_, err := LoadConfig(path)
		require.Error(t, err, replacements)
	}
}

func TestPrivatePathsThroughSymlinks(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "public")
	require.NoError(t, os.Mkdir(public, 0700))
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(public, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// The final database directory does not exist on a fresh installation.
	require.True(t, pathWithin(public, filepath.Join(alias, "new", "db")))
	require.True(t, pathWithin(alias, filepath.Join(public, "new", "db")))
	require.False(t, pathWithin(public, filepath.Join(dir, "public-other", "db")))
}
