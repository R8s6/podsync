package update

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mxpv/podsync/pkg/db"
	"github.com/mxpv/podsync/pkg/feed"
	"github.com/mxpv/podsync/pkg/model"
)

func TestAddWebFeedPersistsAndDeduplicates(t *testing.T) {
	store, err := db.NewBadger(&db.Config{Dir: t.TempDir()})
	require.NoError(t, err)
	defer store.Close()
	key, err := feed.NewFixedKey("test-key")
	require.NoError(t, err)
	manager, err := NewUpdater(make(map[string]*feed.Config), map[model.Provider]feed.KeyProvider{model.ProviderYoutube: key}, "", nil, store, nil)
	require.NoError(t, err)
	defaults := feed.WebDefaults{}
	defaults.ApplyDefaults()
	scheduler := NewScheduler()
	var group sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		group.Go(func() {
			_, err := manager.AddWebFeed(context.Background(), "https://youtube.com/@example/videos?tracking=1", defaults, store, scheduler)
			results <- err
		})
	}
	group.Wait()
	close(results)
	var successes int
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, model.ErrAlreadyExists)
		}
	}
	require.Equal(t, 1, successes)
	saved, err := store.ManagedFeeds()
	require.NoError(t, err)
	require.Len(t, saved, 1)
	require.Len(t, scheduler.pending, 1)
	require.Equal(t, "https://www.youtube.com/@example", saved[0].URL)
	require.Equal(t, defaults.PageSize, saved[0].PageSize)
	require.True(t, saved[0].OPML)
	require.Empty(t, saved[0].PostEpisodeDownload)
	require.Equal(t, []string{"--match-filter", "!is_live"}, saved[0].YouTubeDLArgs)
	_, err = manager.AddWebFeed(context.Background(), "https://www.youtube.com/@example", defaults, store, scheduler)
	require.ErrorIs(t, err, model.ErrAlreadyExists)
	_, err = manager.AddWebFeed(context.Background(), "https://vimeo.com/someuser", defaults, store, scheduler)
	require.ErrorIs(t, err, model.ErrInvalidFeed)
}

type failedFeedStore struct{ count int }

func (s failedFeedStore) ManagedFeeds() ([]*feed.Config, error) {
	return make([]*feed.Config, s.count), nil
}

func (s failedFeedStore) AddManagedFeed(*feed.Config) error { return errors.New("disk full") }

func TestAddWebFeedFailureDoesNotActivate(t *testing.T) {
	key, _ := feed.NewFixedKey("test-key")
	manager, err := NewUpdater(make(map[string]*feed.Config), map[model.Provider]feed.KeyProvider{model.ProviderYoutube: key}, "", nil, nil, nil)
	require.NoError(t, err)
	defaults := feed.WebDefaults{}
	defaults.ApplyDefaults()
	scheduler := NewScheduler()
	for _, count := range []int{0, 100} {
		_, err := manager.AddWebFeed(context.Background(), "https://youtube.com/@example", defaults, failedFeedStore{count}, scheduler)
		require.Error(t, err)
		require.Empty(t, manager.feeds)
		require.Empty(t, scheduler.pending)
		require.Empty(t, scheduler.cron.Entries())
	}
	manager.feeds["manual"] = &feed.Config{ID: "manual", URL: "https://youtube.com/@example/videos"}
	_, err = manager.AddWebFeed(context.Background(), "https://www.youtube.com/@example", defaults, failedFeedStore{}, scheduler)
	require.ErrorIs(t, err, model.ErrAlreadyExists)
}

func TestSchedulerInitialAndRecurringUpdates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		scheduler := NewScheduler()
		require.NoError(t, scheduler.Add(&feed.Config{ID: "regular", UpdatePeriod: 10 * time.Second}))
		require.NoError(t, scheduler.Add(&feed.Config{ID: "explicit", CronSchedule: "@every 10s"}))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := make(chan string, 10)
		done := make(chan error, 1)
		go func() {
			done <- scheduler.Run(ctx, func(_ context.Context, f *feed.Config) error {
				calls <- f.ID
				return nil
			})
		}()
		synctest.Wait()
		require.Equal(t, "regular", <-calls)
		require.Empty(t, calls)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		require.Len(t, calls, 2)
		require.ElementsMatch(t, []string{"regular", "explicit"}, []string{<-calls, <-calls})
		// Feeds can be scheduled while the worker is already running.
		require.NoError(t, scheduler.Add(&feed.Config{ID: "added", UpdatePeriod: time.Hour}))
		synctest.Wait()
		require.Len(t, calls, 1)
		require.Equal(t, "added", <-calls)
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
	})
}

func TestSchedulerCoalescesPendingAndRunningUpdates(t *testing.T) {
	s := NewScheduler()
	f := &feed.Config{ID: "slow", UpdatePeriod: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	s.enqueue(f)
	go func() {
		done <- s.Run(ctx, func(context.Context, *feed.Config) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	for i := 0; i < 50; i++ {
		s.enqueue(f)
	}
	s.mu.Lock()
	require.Empty(t, s.pending)
	s.mu.Unlock()
	cancel()
	close(release)
	require.ErrorIs(t, <-done, context.Canceled)
}
