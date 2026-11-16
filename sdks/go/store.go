package flagnetic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// A store file is a header line naming the key the ruleset was received
// under, then the ruleset document exactly as the endpoint served it. The
// header holds a digest of the key, never the key. Without it, a process
// restarted with another environment's key on the same path would serve that
// environment's ruleset as last_good, and refuse the right one as a
// downgrade whenever the old version was higher.
const storeFormat = 1

type storeHeader struct {
	Format *int   `json:"flagnetic_store"`
	Key    string `json:"key"`
}

var (
	errStoreNoKey    = errors.New("the store was written under no key")
	errStoreOtherKey = errors.New("the store was written under another key")
)

func storeKeyDigest(sdkKey string) string {
	sum := sha256.Sum256([]byte(sdkKey))
	return hex.EncodeToString(sum[:])
}

// encodeStore is the content of a store file holding doc, received under sdkKey.
func encodeStore(sdkKey string, doc []byte) []byte {
	format := storeFormat
	header, _ := json.Marshal(storeHeader{Format: &format, Key: storeKeyDigest(sdkKey)})
	out := make([]byte, 0, len(header)+1+len(doc))
	out = append(out, header...)
	out = append(out, '\n')
	return append(out, doc...)
}

// decodeStore returns the document a store file holds for sdkKey. A file
// without a header, as written before stores were bound to a key, and one
// written under another key are refused.
func decodeStore(sdkKey string, file []byte) ([]byte, error) {
	line, doc, found := bytes.Cut(file, []byte{'\n'})
	var header storeHeader
	if !found || json.Unmarshal(line, &header) != nil || header.Format == nil {
		return nil, errStoreNoKey
	}
	if header.Key != storeKeyDigest(sdkKey) {
		return nil, errStoreOtherKey
	}
	return doc, nil
}

// loadStore seeds the client with the ruleset persisted at the store path
// as last_good. An absent file is a first start; an unreadable or invalid
// one, or one written under another key, logs store_failed and leaves the
// client at built_in.
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
	if doc, err = decodeStore(c.sdkKey, doc); err != nil {
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

// persist writes an accepted ruleset document to the store path under the
// client's key, readable by its owner only since it carries private segments. It replaces the
// file by rename, so a crash never leaves half a document.
func (c *Client) persist(doc []byte) {
	if c.storePath == "" {
		return
	}
	if err := writeFileAtomic(c.storePath, encodeStore(c.sdkKey, doc)); err != nil {
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
