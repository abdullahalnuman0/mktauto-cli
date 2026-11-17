// Package upload sends files straight to storage with the presigned URLs the server hands out (single PUT or multipart).
package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/media"
)

// Parallel parts per file.
const Parallel = 3

// Uploader uploads files for one project; Storage is the client for presigned URLs (no auth header, long timeouts).
type Uploader struct {
	API     *api.Client
	Storage *http.Client
	// Wait between tries (tests make it shorter).
	Backoff []time.Duration
}

func New(c *api.Client) *Uploader {
	return &Uploader{API: c, Storage: &http.Client{Timeout: 30 * time.Minute}, Backoff: []time.Duration{time.Second, 3 * time.Second}}
}

// Upload sends one file and returns the stored media (with the server's measurements: size, duration…).
// progress gets bytes sent so far and the file size.
func (u *Uploader) Upload(ctx context.Context, f media.File, projectID string, progress func(sent, total int64)) (*api.Media, error) {
	up, err := u.API.CreateUpload(ctx, api.UploadRequest{ProjectID: projectID, Filename: f.Name, MimeType: f.Mime, SizeBytes: f.Size, SHA256: f.SHA256})
	if err != nil {
		return nil, err
	}
	m, err := u.send(ctx, f, up, progress)
	if err != nil {
		// Leave nothing half-uploaded behind.
		abortCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_ = u.API.AbortUpload(abortCtx, up.AssetID)
		cancel()
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	return m, nil
}

func (u *Uploader) send(ctx context.Context, f media.File, up *api.UploadResponse, progress func(sent, total int64)) (*api.Media, error) {
	if progress == nil {
		progress = func(int64, int64) {}
	}
	if up.Mode != "MULTIPART" {
		if up.URL == "" {
			return nil, errors.New("the server gave no upload address")
		}
		_, err := u.retrying(ctx, func() (string, error) {
			return u.put(ctx, up.URL, f.Path, 0, f.Size, up.Headers, func(n int64) { progress(n, f.Size) })
		})
		if err != nil {
			return nil, err
		}
		return u.API.CompleteUpload(ctx, up.AssetID, nil)
	}
	if up.PartSize <= 0 || len(up.Parts) == 0 {
		return nil, errors.New("the server gave no upload parts")
	}
	sent := make([]atomic.Int64, len(up.Parts))
	report := func() {
		var total int64
		for i := range sent {
			total += sent[i].Load()
		}
		progress(total, f.Size)
	}
	parts := make([]api.CompletedPart, len(up.Parts))
	sem := make(chan struct{}, Parallel)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for i, p := range up.Parts {
		wg.Add(1)
		go func(i int, p api.UploadPart) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			offset := int64(p.PartNumber-1) * up.PartSize
			length := min(up.PartSize, f.Size-offset)
			etag, err := u.retrying(ctx, func() (string, error) {
				return u.put(ctx, p.URL, f.Path, offset, length, nil, func(n int64) { sent[i].Store(n); report() })
			})
			if err == nil && etag == "" {
				err = errors.New("storage didn't return the part's ETag (the bucket's CORS must expose ETag)")
			}
			if err != nil {
				once.Do(func() { firstErr = err; cancel() })
				return
			}
			parts[i] = api.CompletedPart{PartNumber: p.PartNumber, ETag: etag}
		}(i, p)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	sort.Slice(parts, func(a, b int) bool { return parts[a].PartNumber < parts[b].PartNumber })
	return u.API.CompleteUpload(ctx, up.AssetID, parts)
}

// statusError is an HTTP failure from storage.
type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("storage answered HTTP %d", e.code) }

// retrying tries up to 3 times for network errors and server-side failures (5xx, 408, 429), never for other answers.
func (u *Uploader) retrying(ctx context.Context, f func() (string, error)) (string, error) {
	var err error
	for attempt := 0; ; attempt++ {
		var out string
		out, err = f()
		if err == nil {
			return out, nil
		}
		var se *statusError
		if errors.As(err, &se) && se.code < 500 && se.code != 408 && se.code != 429 {
			return "", err
		}
		if ctx.Err() != nil || attempt >= len(u.Backoff) {
			return "", err
		}
		select {
		case <-time.After(u.Backoff[attempt]):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// put sends bytes [offset, offset+length) of the file and returns the ETag.
func (u *Uploader) put(ctx context.Context, url, path string, offset, length int64, headers map[string]string, onSent func(int64)) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	body := &counting{r: io.NewSectionReader(file, offset, length), onRead: onSent}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return "", err
	}
	req.ContentLength = length
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := u.Storage.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 300 {
		return "", &statusError{res.StatusCode}
	}
	return res.Header.Get("ETag"), nil
}

type counting struct {
	r      io.Reader
	n      int64
	onRead func(int64)
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.onRead != nil {
		c.onRead(c.n)
	}
	return n, err
}
