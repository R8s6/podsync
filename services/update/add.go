package update

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/mxpv/podsync/pkg/builder"
	"github.com/mxpv/podsync/pkg/feed"
	"github.com/mxpv/podsync/pkg/model"
)

type ManagedFeedStore interface {
	ManagedFeeds() ([]*feed.Config, error)
	AddManagedFeed(*feed.Config) error
}

// AddWebFeed persists before scheduling. A failed write cannot create an active
// feed, and a crash after the write is recovered by loading definitions at startup.
func (u *Manager) AddWebFeed(ctx context.Context, link string, defaults feed.WebDefaults, store ManagedFeedStore, scheduler *Scheduler) (string, error) {
	canonical, err := builder.CanonicalFeedURL(link)
	if err != nil {
		return "", err
	}
	info, _ := builder.ParseURL(canonical)
	if _, ok := u.keys[info.Provider]; !ok {
		return "", fmt.Errorf("%w: configure the provider API key on the host first", model.ErrInvalidFeed)
	}
	u.feedsMu.Lock()
	defer u.feedsMu.Unlock()
	for _, f := range u.feeds {
		if existing, err := builder.CanonicalFeedURL(f.URL); err == nil && canonical == existing {
			return "", model.ErrAlreadyExists
		}
	}
	managed, err := store.ManagedFeeds()
	if err != nil {
		return "", err
	}
	if len(managed) >= 100 {
		return "", model.ErrFeedLimit
	}
	id := fmt.Sprintf("web_%x", sha256.Sum256([]byte(canonical)))[:28]
	if _, exists := u.feeds[id]; exists {
		return "", model.ErrAlreadyExists
	}
	f := defaults.Feed(id, canonical)
	schedule, err := feedSchedule(f)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := store.AddManagedFeed(f); err != nil {
		return "", err
	}
	u.feeds[id] = f
	scheduler.register(f, schedule, true)
	return id, nil
}
