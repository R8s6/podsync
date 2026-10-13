package db

import (
	"github.com/dgraph-io/badger"
	"github.com/mxpv/podsync/pkg/feed"
)

// Managed feed definitions are separate from downloaded feed metadata. They
// survive restarts and disabling web administration, and contain no credentials.
func (b *Badger) ManagedFeeds() ([]*feed.Config, error) {
	var feeds []*feed.Config
	err := b.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = b.getKey("managed_feed/")
		return b.iterator(txn, opts, func(item *badger.Item) error {
			f := &feed.Config{}
			if err := b.unmarshalObj(item, f); err != nil {
				return err
			}
			feeds = append(feeds, f)
			return nil
		})
	})
	return feeds, err
}

func (b *Badger) AddManagedFeed(f *feed.Config) error {
	return b.db.Update(func(txn *badger.Txn) error {
		return b.setObj(txn, b.getKey("managed_feed/%s", f.ID), f, false)
	})
}
