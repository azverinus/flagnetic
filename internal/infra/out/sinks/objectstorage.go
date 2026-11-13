package sinks

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

// ObjectStorage delivers batches to a bucket of an S3-compatible service as
// one file each: a successful PUT is the acknowledgement. Every call it makes,
// a probe's, a sample's and a batch's, goes through the same client, so they
// cannot drift apart.
type ObjectStorage struct{ transport http.RoundTripper }

var _ appsinks.Target = (*ObjectStorage)(nil)

// NewObjectStorage returns the target calling buckets through client's
// transport, which is what guards the addresses it may dial.
func NewObjectStorage(client *http.Client) *ObjectStorage {
	return &ObjectStorage{transport: client.Transport}
}

// Probe shows whether the bucket takes a file, by writing the probe object
// under the sink's prefix and deleting it again. An object storage target has
// nothing to prepare.
func (o *ObjectStorage) Probe(ctx context.Context, c appsinks.Call, _ time.Time, _ bool) appsinks.Probed {
	cfg := *c.Sink.Config.ObjectStorage
	b, failure := o.bucket(cfg, c.Secrets)
	if failure != nil {
		return appsinks.Probed{Failure: failure}
	}
	key := cfg.ProbeKey()
	if f := b.put(ctx, key, []byte("flagnetic test connection\n"), "text/plain"); f != nil {
		return appsinks.Probed{Failure: f}
	}
	return appsinks.Probed{Failure: b.remove(ctx, key)}
}

// Deliver writes the batch as one file under the key scheme of the sink's
// prefix, in its format, and answers why the service did not take it. A batch
// with no rows is acknowledged without a file: retention can have dropped
// every row of it between attempts.
func (o *ObjectStorage) Deliver(ctx context.Context, c appsinks.Call, d appsinks.Delivery) *sinks.Failure {
	if len(d.Rows) == 0 {
		return nil
	}
	cfg := *c.Sink.Config.ObjectStorage
	key := cfg.BatchKey(d.Kind, c.ProjectKey, d.ID, d.Rows)
	if d.Sample {
		key = cfg.SampleKey(d.Kind, c.ProjectKey, d.ID, d.Now, d.Rows)
	}
	b, failure := o.bucket(cfg, c.Secrets)
	if failure != nil {
		return failure
	}
	body, err := encodeFile(cfg, d.Rows)
	if err != nil {
		// A batch that cannot be encoded is the platform's own failure, which
		// a retry may cure once it is fixed; it is no answer of the bucket's.
		return &sinks.Failure{Class: sinks.ClassConnect}
	}
	_, contentType := cfg.FileType()
	return b.put(ctx, key, body, contentType)
}

// bucket is one configured bucket, as a sink reaches it.
type bucket struct {
	client *minio.Client
	name   string
}

// bucket returns cfg's bucket at its endpoint, or AWS when there is none,
// addressed by path or by virtual host as the sink says, and signed with its
// access key and secret. The region is asked of the bucket when empty. The
// client retries nothing itself: the delivery worker retries with backoff, and
// a probe answers at once.
func (o *ObjectStorage) bucket(cfg sinks.ObjectStorage, secrets []string) (bucket, *sinks.Failure) {
	host, secure := "s3.amazonaws.com", true
	if cfg.Endpoint != "" {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || u.Host == "" {
			return bucket{}, &sinks.Failure{Class: sinks.ClassConnect}
		}
		host, secure = u.Host, u.Scheme == "https"
	}
	lookup := minio.BucketLookupDNS
	if cfg.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, secrets[0], ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: lookup,
		Transport:    o.transport,
		MaxRetries:   1,
	})
	if err != nil {
		return bucket{}, &sinks.Failure{Class: sinks.ClassConnect}
	}
	return bucket{client: client, name: cfg.Bucket}, nil
}

// put writes body as the one object key, in a single request whatever its
// size, since a single PUT takes up to 5 GiB: its success is the
// acknowledgement, and a failed attempt leaves no incomplete upload behind.
func (b bucket) put(ctx context.Context, key string, body []byte, contentType string) *sinks.Failure {
	ctx, cancel := context.WithTimeout(ctx, sinks.KindObjectStorage.CallTimeout())
	defer cancel()
	_, err := b.client.PutObject(ctx, b.name, key, bytes.NewReader(body), int64(len(body)),
		minio.PutObjectOptions{ContentType: contentType, DisableMultipart: true})
	return failureOf(err)
}

func (b bucket) remove(ctx context.Context, key string) *sinks.Failure {
	ctx, cancel := context.WithTimeout(ctx, sinks.KindObjectStorage.CallTimeout())
	defer cancel()
	return failureOf(b.client.RemoveObject(ctx, b.name, key, minio.RemoveObjectOptions{}))
}

// failureOf reduces why a call failed to a class: the service's own answer is
// classified by its status and the code of its error document, and a call that
// got no answer by why. What the service wrote in words is not kept.
func failureOf(err error) *sinks.Failure {
	if err == nil {
		return nil
	}
	var answer minio.ErrorResponse
	if errors.As(err, &answer) && answer.StatusCode != 0 {
		if f, failed := sinks.S3Failure(answer.StatusCode, answer.Code); failed {
			return &f
		}
	}
	f := ClassifyError(err)
	return &f
}
