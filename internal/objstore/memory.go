package objstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type memObject struct {
	data        []byte
	contentType string
	etag        string
}

// memoryStore is a process-local Store used by unit tests that need real
// Put/Get round trips without a network dependency on MinIO. It implements
// the same Range-request and presign contracts as the S3 backend closely
// enough for those tests, but PresignPut here returns an unusable placeholder
// URL — anything that exercises a real HTTP PUT round trip must run against
// the real MinIO-backed Store in internal/objstore/store_test.go instead.
type memoryStore struct {
	mu      sync.RWMutex
	objects map[string]memObject
}

// NewMemory returns an in-memory Store for unit tests.
func NewMemory() Store {
	return &memoryStore{objects: map[string]memObject{}}
}

func (m *memoryStore) Put(_ context.Context, key, contentType string, r io.Reader, size int64) error {
	if key == "" {
		return fmt.Errorf("objstore: key is required")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if size >= 0 && int64(len(data)) != size {
		return fmt.Errorf("objstore: declared size %d does not match %d bytes written", size, len(data))
	}
	sum := sha256.Sum256(data)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memObject{data: data, contentType: contentType, etag: hex.EncodeToString(sum[:])}
	return nil
}

func (m *memoryStore) Get(_ context.Context, key string, rangeHeader string) (*Object, error) {
	m.mu.RLock()
	obj, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotExist
	}
	data := obj.data
	status := http.StatusOK
	var contentRange string
	if rangeHeader != "" {
		start, end, ok := parseByteRange(rangeHeader, len(data))
		if ok {
			contentRange = fmt.Sprintf("bytes %d-%d/%d", start, end, len(data))
			data = data[start : end+1]
			status = http.StatusPartialContent
		}
	}
	return &Object{
		Body:          io.NopCloser(bytes.NewReader(data)),
		ContentType:   obj.contentType,
		ContentLength: int64(len(data)),
		ContentRange:  contentRange,
		ETag:          obj.etag,
		StatusCode:    status,
	}, nil
}

func (m *memoryStore) Head(_ context.Context, key string) (*Object, error) {
	m.mu.RLock()
	obj, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotExist
	}
	return &Object{
		ContentType:   obj.contentType,
		ContentLength: int64(len(obj.data)),
		ETag:          obj.etag,
		StatusCode:    http.StatusOK,
	}, nil
}

func (m *memoryStore) Delete(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.objects, k) // deleting an absent key is a no-op success
	}
	return nil
}

func (m *memoryStore) PresignPut(_ context.Context, key, contentType string, size int64, ttl time.Duration) (string, map[string]string, error) {
	return "memory://" + key, map[string]string{
		"Content-Type":   contentType,
		"Content-Length": strconv.FormatInt(size, 10),
	}, nil
}

func (m *memoryStore) Healthy(context.Context) error { return nil }

// parseByteRange parses a single-range "bytes=start-end" header, as produced
// by clients seeking within a video. Only a single range is supported, which
// is all this product's media proxy ever needs.
func parseByteRange(header string, size int) (start, end int, ok bool) {
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(header, prefix)
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	if parts[0] == "" {
		// Suffix range "bytes=-N": last N bytes.
		n, err := strconv.Atoi(parts[1])
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true
	}
	s, err := strconv.Atoi(parts[0])
	if err != nil || s < 0 || s >= size {
		return 0, 0, false
	}
	e := size - 1
	if parts[1] != "" {
		e, err = strconv.Atoi(parts[1])
		if err != nil || e < s {
			return 0, 0, false
		}
		if e >= size {
			e = size - 1
		}
	}
	return s, e, true
}
