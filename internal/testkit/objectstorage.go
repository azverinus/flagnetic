package testkit

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// minioImage is the MinIO every object storage test meets, pinned so that a
// run never drifts with a moving tag. It is the last release MinIO published
// as an image, and from quay.io, since Docker Hub no longer carries it.
// Replacing it touches this constant alone.
const minioImage = "quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z"

// ObjectStorage is the package's S3-compatible store: MinIO in a container, or
// the one S3_ENDPOINT names.
type ObjectStorage struct {
	// Endpoint is the store's address as a sink's endpoint names it,
	// http://localhost:<port>, so that a virtual-hosted request for
	// <bucket>.localhost resolves to it as well.
	Endpoint string
	// Region is the region the store answers for.
	Region string
	// AccessKeyID and SecretAccessKey are the credentials a sink signs with.
	AccessKeyID, SecretAccessKey string

	client *minio.Client
}

var objectStorage struct {
	mu      sync.Mutex
	store   *ObjectStorage
	stopper func()
}

// stopObjectStorage ends the container the package started.
func stopObjectStorage() {
	objectStorage.mu.Lock()
	defer objectStorage.mu.Unlock()
	if objectStorage.stopper != nil {
		objectStorage.stopper()
	}
	objectStorage.store, objectStorage.stopper = nil, nil
}

// StartObjectStorage returns the package's object store, starting it on first
// use: the one S3_ENDPOINT names, with S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY
// and S3_REGION (us-east-1 when unset), or a MinIO container stopped after the
// package's last test. A store S3_ENDPOINT names must accept virtual-hosted
// requests for <bucket>.localhost (MinIO's MINIO_DOMAIN=localhost) for the
// path style test to pass. Tests share it and isolate by bucket.
func StartObjectStorage(t testing.TB) *ObjectStorage {
	t.Helper()
	objectStorage.mu.Lock()
	defer objectStorage.mu.Unlock()
	if objectStorage.store != nil {
		return objectStorage.store
	}
	s := &ObjectStorage{Region: "us-east-1"}
	if raw := os.Getenv("S3_ENDPOINT"); raw != "" {
		s.Endpoint = strings.TrimSuffix(raw, "/")
		s.AccessKeyID, s.SecretAccessKey = os.Getenv("S3_ACCESS_KEY_ID"), os.Getenv("S3_SECRET_ACCESS_KEY")
		if r := os.Getenv("S3_REGION"); r != "" {
			s.Region = r
		}
	} else {
		s.AccessKeyID, s.SecretAccessKey = "flagnetic", "minio-test-"+strings.ToLower(rand.Text())[:16]
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		c, err := testcontainers.Run(ctx, minioImage,
			testcontainers.WithExposedPorts("9000/tcp"),
			testcontainers.WithEnv(map[string]string{
				"MINIO_ROOT_USER":     s.AccessKeyID,
				"MINIO_ROOT_PASSWORD": s.SecretAccessKey,
				"MINIO_DOMAIN":        "localhost",
				"MINIO_REGION":        s.Region,
			}),
			testcontainers.WithCmd("server", "/data"),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/minio/health/live").WithPort("9000/tcp").WithStartupTimeout(2*time.Minute)),
		)
		if c != nil {
			objectStorage.stopper = func() { _ = testcontainers.TerminateContainer(c) }
		}
		if err != nil {
			t.Fatalf("starting MinIO (is Docker running? or set S3_ENDPOINT): %v", err)
		}
		port, err := c.MappedPort(ctx, "9000/tcp")
		if err != nil {
			t.Fatal(err)
		}
		s.Endpoint = "http://localhost:" + port.Port()
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil {
		t.Fatalf("S3_ENDPOINT is no URL: %v", err)
	}
	s.client, err = minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(s.AccessKeyID, s.SecretAccessKey, ""),
		Secure:       u.Scheme == "https",
		Region:       s.Region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	objectStorage.store = s
	return s
}

// NewBucket creates a bucket named for no other test, so that tests sharing the
// store never see each other's objects.
func (s *ObjectStorage) NewBucket(t testing.TB) string {
	t.Helper()
	name := "test-" + strings.ToLower(rand.Text())[:16]
	if err := s.client.MakeBucket(t.Context(), name, minio.MakeBucketOptions{Region: s.Region}); err != nil {
		t.Fatalf("creating bucket %s: %v", name, err)
	}
	return name
}

// Keys lists every object key in bucket, sorted.
func (s *ObjectStorage) Keys(t testing.TB, bucket string) []string {
	t.Helper()
	var keys []string
	for obj := range s.client.ListObjects(t.Context(), bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			t.Fatalf("listing %s: %v", bucket, obj.Err)
		}
		keys = append(keys, obj.Key)
	}
	slices.Sort(keys)
	return keys
}

// Object reads the content of key in bucket, and its content type.
func (s *ObjectStorage) Object(t testing.TB, bucket, key string) (content []byte, contentType string) {
	t.Helper()
	obj, err := s.client.GetObject(t.Context(), bucket, key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("getting %s/%s: %v", bucket, key, err)
	}
	defer func() { _ = obj.Close() }()
	content, err = io.ReadAll(obj)
	if err != nil {
		t.Fatalf("reading %s/%s: %v", bucket, key, err)
	}
	info, err := obj.Stat()
	if err != nil {
		t.Fatalf("stat %s/%s: %v", bucket, key, err)
	}
	return content, info.ContentType
}

// S3Request is a request an S3Proxy forwarded.
type S3Request struct {
	Method, Host, Path string
	// Status is what the proxy answered the client.
	Status int
}

// S3Proxy is a proxy in front of the object store that records every request
// it forwards and can fail the next ones of a method after the store has
// handled them, as an answer lost on the way back would. It keeps the Host a
// sink signed, so the store verifies the signature made for the proxy's own
// address.
type S3Proxy struct {
	// Endpoint is the proxy's address as a sink's endpoint names it.
	Endpoint string

	mu       sync.Mutex
	requests []S3Request
	failNext map[string]int
}

// NewS3Proxy starts a proxy in front of s, closed when t ends.
func NewS3Proxy(t testing.TB, s *ObjectStorage) *S3Proxy {
	t.Helper()
	backend, err := url.Parse(s.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	p := &S3Proxy{failNext: map[string]int{}}
	rp := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(backend)
		r.Out.Host = r.In.Host
	}}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := p.takeFailure(r.Method)
		rec := httptest.NewRecorder()
		rp.ServeHTTP(rec, r)
		status := rec.Code
		if fail {
			status = http.StatusInternalServerError
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>InternalError</Code><Message>injected by the test proxy</Message></Error>`)
		} else {
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(status)
			_, _ = io.Copy(w, bytes.NewReader(rec.Body.Bytes()))
		}
		p.mu.Lock()
		p.requests = append(p.requests, S3Request{Method: r.Method, Host: r.Host, Path: r.URL.Path, Status: status})
		p.mu.Unlock()
	}))
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	p.Endpoint = fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port)
	return p
}

// FailNext makes the next n requests of method fail with a 500 after the store
// has handled them.
func (p *S3Proxy) FailNext(method string, n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNext[method] += n
}

func (p *S3Proxy) takeFailure(method string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failNext[method] == 0 {
		return false
	}
	p.failNext[method]--
	return true
}

// Requests lists every request forwarded so far, in order.
func (p *S3Proxy) Requests() []S3Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.requests)
}
