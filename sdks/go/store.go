package flagnetic

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// loadStore seeds the client with the ruleset persisted at the store path
// as last_good. An absent file is a first start; an unreadable or invalid
// one logs store_failed and leaves the client at built_in.
func (c *Client) loadStore() {
	info, err := os.Stat(c.storePath)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var doc []byte
	if err == nil {
		doc, err = os.ReadFile(c.storePath)
	}
	if err != nil {
		c.storeFailed("load", err)
		return
	}
	rs, _, err := decodeServerRuleset(doc)
	if err != nil {
		c.storeFailed("load", err)
		return
	}
	// The file was written when the ruleset was received, so its age shows
	// through ReceivedAt.
	c.swap(rs, "store", info.ModTime())
	c.setStage(StageLastGood)
}

// persist writes an accepted ruleset document to the store path, readable
// by its owner only since it carries private segments. It replaces the
// file by rename, so a crash never leaves half a document.
func (c *Client) persist(doc []byte) {
	if c.storePath == "" {
		return
	}
	if err := writeFileAtomic(c.storePath, doc); err != nil {
		c.storeFailed("save", err)
	}
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (c *Client) storeFailed(op string, err error) {
	c.log(slog.LevelWarn, EventStoreFailed, map[string]any{"path": c.storePath, "op": op, "cause": err.Error()})
}
