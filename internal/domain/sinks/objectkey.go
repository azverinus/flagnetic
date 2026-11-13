package sinks

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// ProbeObject is the name, under a sink's prefix, of the object a test of an
// object storage sink writes and deletes again.
const ProbeObject = "_flagnetic_probe"

// FileType returns the extension and the content type of the files c writes:
// JSON Lines compressed with gzip or zstd or not at all, or Parquet.
func (c ObjectStorage) FileType() (extension, contentType string) {
	if c.Format == "parquet" {
		return "parquet", "application/vnd.apache.parquet"
	}
	switch c.Compression {
	case "zstd":
		return "jsonl.zst", "application/zstd"
	case "none":
		return "jsonl", "application/x-ndjson"
	default:
		return "jsonl.gz", "application/gzip"
	}
}

// ProbeKey returns the key of the probe object.
func (c ObjectStorage) ProbeKey() string { return c.under(ProbeObject) }

// BatchKey returns the key the file of a batch of kind is written under:
//
//	{prefix}/{event_kind}/dt={UTC day of the first row's received_at}/{project_key}-{first_seq}-{last_seq}-{hash}.{ext}
//
// where hash is the delivery ID without its prefix. The delivery ID names one
// set of rows, so a retry of a batch overwrites its own file with the same
// rows, while other rows over the same range, a replay under a narrower
// filter, land beside it. rows is not empty.
func (c ObjectStorage) BatchKey(kind EventKind, projectKey, deliveryID string, rows []Row) string {
	first, last := rows[0].Sequence(), rows[len(rows)-1].Sequence()
	name := projectKey + "-" + strconv.FormatInt(first, 10) + "-" + strconv.FormatInt(last, 10) + "-" +
		strings.TrimPrefix(deliveryID, "dlv_")
	return c.fileKey(kind, rows[0].Received(), name)
}

// SampleKey returns the key the file of a sample of kind is written under: in
// the partition a consumer already reads, told apart by a marker and the time
// at which it was sent in place of the sequence range. A sample's delivery ID
// is not a hash, so the name carries one of it, of the width of a delivery
// ID's. rows is not empty.
func (c ObjectStorage) SampleKey(kind EventKind, projectKey, deliveryID string, at time.Time, rows []Row) string {
	sum := sha256.Sum256([]byte(deliveryID))
	name := projectKey + "-sample-" + at.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(sum[:13])
	return c.fileKey(kind, rows[0].Received(), name)
}

func (c ObjectStorage) fileKey(kind EventKind, firstReceived time.Time, name string) string {
	extension, _ := c.FileType()
	return c.under(string(kind) + "/dt=" + firstReceived.UTC().Format("2006-01-02") + "/" + name + "." + extension)
}

// under returns rest under the prefix, which carries no slash of its own into
// the key.
func (c ObjectStorage) under(rest string) string {
	if prefix := strings.Trim(c.Prefix, "/"); prefix != "" {
		return prefix + "/" + rest
	}
	return rest
}
