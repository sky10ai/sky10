// Package s3 implements adapter.Backend for S3-compatible storage.
//
// It works with any S3-compatible provider: AWS S3, Backblaze B2,
// Cloudflare R2, MinIO, etc. Configure via endpoint URL.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/sky10/sky10/pkg/adapter"
	"github.com/sky10/sky10/pkg/transfer"
)

// Backend stores encrypted blobs in an S3-compatible bucket.
// All S3 calls go through a semaphore to prevent connection pool exhaustion.
type Backend struct {
	client *s3.Client
	bucket string
	sem    chan struct{} // concurrency limiter
	logger *slog.Logger
}

// SetLogger sets the logger for S3 request/response logging.
func (b *Backend) SetLogger(logger *slog.Logger) {
	b.logger = logger
}

// Config holds S3 connection parameters.
type Config struct {
	Bucket          string
	Region          string
	Endpoint        string // custom endpoint for B2/R2/MinIO
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool // required for MinIO and some S3-compatible stores
}

// New creates an S3 backend from the given config.
func New(ctx context.Context, cfg Config) (*Backend, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3: bucket is required")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(cfg.Region),
	}

	// Resolve credentials: Config fields → S3_* env vars → AWS_* env vars (SDK default)
	accessKey := cfg.AccessKeyID
	secretKey := cfg.SecretAccessKey
	if accessKey == "" {
		accessKey = os.Getenv("S3_ACCESS_KEY_ID")
	}
	if secretKey == "" {
		secretKey = os.Getenv("S3_SECRET_ACCESS_KEY")
	}

	if accessKey != "" && secretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	// Transport-level timeouts for connection setup and idle management.
	// No http.Client.Timeout — that's a wall-clock timeout that kills
	// active transfers of large files. TCP keepalive (Go default: 30s)
	// detects dead connections instead.
	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:          20,
			MaxIdleConnsPerHost:   10,
			MaxConnsPerHost:       10,
			ResponseHeaderTimeout: 15 * time.Second,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
		},
	}
	opts = append(opts, config.WithHTTPClient(httpClient))

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3: loading config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = cfg.ForcePathStyle
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)

	return &Backend{
		client: client,
		bucket: cfg.Bucket,
		sem:    make(chan struct{}, 5), // max 5 concurrent S3 calls
		logger: slog.Default(),
	}, nil
}

const s3Timeout = 30 * time.Second

func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s3Timeout)
}

func (b *Backend) acquire(ctx context.Context) error {
	select {
	case b.sem <- struct{}{}:
		return nil
	default:
		// Semaphore full — log and wait with hard 15s max
		b.logger.Debug("s3 sem wait", "in_use", len(b.sem), "cap", cap(b.sem))
		start := time.Now()
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		select {
		case b.sem <- struct{}{}:
			b.logger.Debug("s3 sem acquired", "waited_ms", time.Since(start).Milliseconds())
			return nil
		case <-ctx.Done():
			b.logger.Warn("s3 sem ctx cancelled", "waited_ms", time.Since(start).Milliseconds())
			return ctx.Err()
		case <-timer.C:
			b.logger.Warn("s3 sem hard timeout (15s) — slots likely leaked")
			return fmt.Errorf("s3: semaphore acquire timeout (15s)")
		}
	}
}

func (b *Backend) release() {
	<-b.sem
}

// Put stores data from r under the given key.
func (b *Backend) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := b.acquire(ctx); err != nil {
		return fmt.Errorf("s3: put %q: %w", key, err)
	}
	defer b.release()
	b.logger.Debug("s3 PUT", "key", key, "size", size)
	start := time.Now()

	// Stall detection: wrap the request body so we can detect when the
	// HTTP client stops reading (socket write blocked on dead connection).
	// If no Read() call happens for 30s, cancel the request.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tr := transfer.NewReader(r, size)
	tr.SetIdleTimeout(30 * time.Second)
	tr.OnStall(cancel)
	defer tr.Close()

	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(b.bucket),
		Key:           aws.String(key),
		Body:          tr,
		ContentLength: aws.Int64(size),
	})
	if err != nil {
		if tr.Stalled() {
			err = transfer.ErrIdleTimeout
		}
		b.logger.Warn("s3 PUT failed", "key", key, "error", err, "ms", time.Since(start).Milliseconds())
		return fmt.Errorf("s3: put %q: %w", key, err)
	}
	b.logger.Debug("s3 PUT ok", "key", key, "ms", time.Since(start).Milliseconds())
	return nil
}

// Get returns a reader for the data stored at key.
// The caller must close the returned ReadCloser.
func (b *Backend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := b.acquire(ctx); err != nil {
		return nil, fmt.Errorf("s3: get %q: %w", key, err)
	}
	// release happens in cancelOnClose.Close()
	b.logger.Debug("s3 GET", "key", key)
	start := time.Now()
	// No wall-clock timeout — body reads need time for large chunks.
	// TCP keepalive detects dead connections. Caller's context can
	// still cancel if needed.
	ctx, cancel := context.WithCancel(ctx)
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		cancel()
		b.release()
		if isNotFound(err) {
			b.logger.Debug("s3 GET not found", "key", key, "ms", time.Since(start).Milliseconds())
			return nil, adapter.ErrNotFound
		}
		b.logger.Warn("s3 GET failed", "key", key, "error", err, "ms", time.Since(start).Milliseconds())
		return nil, fmt.Errorf("s3: get %q: %w", key, err)
	}
	b.logger.Debug("s3 GET ok", "key", key, "ms", time.Since(start).Milliseconds())
	return &cancelOnClose{ReadCloser: out.Body, cancel: cancel, release: b.release}, nil
}

// cancelOnClose wraps a ReadCloser, cancels context and releases semaphore on Close.
type cancelOnClose struct {
	io.ReadCloser
	cancel  context.CancelFunc
	release func()
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	c.release()
	return err
}

// Delete removes the object at key.
func (b *Backend) Delete(ctx context.Context, key string) error {
	// Check existence first — S3 DeleteObject is idempotent and won't error
	// on missing keys, but our interface requires ErrNotFound.
	if _, err := b.Head(ctx, key); err != nil {
		return err
	}

	if err := b.acquire(ctx); err != nil {
		return fmt.Errorf("s3: delete %q: %w", key, err)
	}
	defer b.release()
	b.logger.Debug("s3 DELETE", "key", key)
	start := time.Now()
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		b.logger.Warn("s3 DELETE failed", "key", key, "error", err, "ms", time.Since(start).Milliseconds())
		return fmt.Errorf("s3: delete %q: %w", key, err)
	}
	b.logger.Debug("s3 DELETE ok", "key", key, "ms", time.Since(start).Milliseconds())
	return nil
}

// List returns all keys with the given prefix.
func (b *Backend) List(ctx context.Context, prefix string) ([]string, error) {
	if err := b.acquire(ctx); err != nil {
		return nil, fmt.Errorf("s3: list %q: %w", prefix, err)
	}
	defer b.release()
	b.logger.Debug("s3 LIST", "prefix", prefix)
	start := time.Now()
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var keys []string
	paginator := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(b.bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			b.logger.Warn("s3 LIST failed", "prefix", prefix, "error", err, "ms", time.Since(start).Milliseconds())
			return nil, fmt.Errorf("s3: list %q: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
	}

	sort.Strings(keys)
	b.logger.Debug("s3 LIST ok", "prefix", prefix, "count", len(keys), "ms", time.Since(start).Milliseconds())
	return keys, nil
}

// Head returns metadata for the object at key.
func (b *Backend) Head(ctx context.Context, key string) (adapter.ObjectMeta, error) {
	if err := b.acquire(ctx); err != nil {
		return adapter.ObjectMeta{}, fmt.Errorf("s3: head %q: %w", key, err)
	}
	defer b.release()
	b.logger.Debug("s3 HEAD", "key", key)
	start := time.Now()
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			b.logger.Debug("s3 HEAD not found", "key", key, "ms", time.Since(start).Milliseconds())
			return adapter.ObjectMeta{}, adapter.ErrNotFound
		}
		b.logger.Warn("s3 HEAD failed", "key", key, "error", err, "ms", time.Since(start).Milliseconds())
		return adapter.ObjectMeta{}, fmt.Errorf("s3: head %q: %w", key, err)
	}
	b.logger.Debug("s3 HEAD ok", "key", key, "ms", time.Since(start).Milliseconds())

	meta := adapter.ObjectMeta{
		Key:  key,
		Size: aws.ToInt64(out.ContentLength),
	}
	if out.LastModified != nil {
		meta.LastModified = *out.LastModified
	}
	return meta, nil
}

// GetRange returns a reader for a byte range within the object.
func (b *Backend) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	rangeHeader := fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
		Range:  aws.String(rangeHeader),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, adapter.ErrNotFound
		}
		return nil, fmt.Errorf("s3: get-range %q: %w", key, err)
	}
	return out.Body, nil
}

// isNotFound checks if an error indicates the object doesn't exist.
func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nsb *types.NotFound
	if errors.As(err, &nsb) {
		return true
	}
	return false
}

// Ensure Backend implements adapter.Backend at compile time.
var _ adapter.Backend = (*Backend)(nil)

// NewMemory creates an in-memory backend for testing.
func NewMemory() *MemoryBackend {
	return &MemoryBackend{
		objects: make(map[string][]byte),
	}
}

// MemoryBackend is an in-memory implementation of adapter.Backend for tests.
// Thread-safe — all methods are protected by a mutex.
type MemoryBackend struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

// Put stores data in memory.
func (m *MemoryBackend) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("memory: reading data: %w", err)
	}
	m.mu.Lock()
	m.objects[key] = data
	m.mu.Unlock()
	return nil
}

// Get returns data from memory.
func (m *MemoryBackend) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.RLock()
	data, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return nil, adapter.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Delete removes data from memory.
func (m *MemoryBackend) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[key]; !ok {
		return adapter.ErrNotFound
	}
	delete(m.objects, key)
	return nil
}

// List returns keys matching the prefix.
func (m *MemoryBackend) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	var keys []string
	for k := range m.objects {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	m.mu.RUnlock()
	sort.Strings(keys)
	return keys, nil
}

// Head returns metadata for a key.
func (m *MemoryBackend) Head(_ context.Context, key string) (adapter.ObjectMeta, error) {
	m.mu.RLock()
	data, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return adapter.ObjectMeta{}, adapter.ErrNotFound
	}
	return adapter.ObjectMeta{
		Key:  key,
		Size: int64(len(data)),
	}, nil
}

// GetRange returns a reader for a byte range.
func (m *MemoryBackend) GetRange(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, adapter.ErrNotFound
	}
	end := offset + length
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	if offset >= int64(len(data)) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	return io.NopCloser(bytes.NewReader(data[offset:end])), nil
}

var _ adapter.Backend = (*MemoryBackend)(nil)
