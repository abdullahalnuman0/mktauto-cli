package upload

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/media"
)

type fake struct {
	mu        sync.Mutex
	srv       *httptest.Server
	parts     int
	failOnce  map[int]bool
	failAll   bool
	got       map[string]int64
	completed []api.CompletedPart
	aborted   int
	request   api.UploadRequest
}

func newFake(t *testing.T, parts int) *fake {
	f := &fake{parts: parts, failOnce: map[int]bool{}, got: map[string]int64{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/api/media/uploads":
			_ = json.NewDecoder(r.Body).Decode(&f.request)
			if f.parts == 0 {
				fmt.Fprintf(w, `{"assetId":"a1","mode":"SINGLE","url":"%s/r2/single","headers":{"Content-Type":"image/png"}}`, f.srv.URL)
				return
			}
			var ps []string
			for i := 1; i <= f.parts; i++ {
				ps = append(ps, fmt.Sprintf(`{"partNumber":%d,"url":"%s/r2/part/%d"}`, i, f.srv.URL, i))
			}
			fmt.Fprintf(w, `{"assetId":"a1","mode":"MULTIPART","partSize":4,"parts":[%s]}`, strings.Join(ps, ","))
		case strings.HasPrefix(r.URL.Path, "/r2/"):
			n := 0
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/r2/part/"), "%d", &n)
			body, _ := io.ReadAll(r.Body)
			if f.failAll || f.failOnce[n] {
				delete(f.failOnce, n)
				w.WriteHeader(503)
				return
			}
			f.got[r.URL.Path] = int64(len(body))
			w.Header().Set("ETag", fmt.Sprintf(`"e%d"`, n))
		case r.URL.Path == "/api/media/a1/complete":
			var body struct{ Parts []api.CompletedPart }
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.completed = body.Parts
			fmt.Fprint(w, `{"id":"a1","kind":"IMAGE","storageStatus":"STORED"}`)
		case r.URL.Path == "/api/media/a1/abort":
			f.aborted++
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func file(t *testing.T, size int) media.File {
	p := filepath.Join(t.TempDir(), "f.png")
	_ = os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o600)
	return media.File{Path: p, Name: "f.png", Mime: "image/png", Size: int64(size), SHA256: strings.Repeat("a", 64)}
}

func uploader(f *fake) *Uploader {
	u := New(api.New(f.srv.URL, "mkc_t", "test"))
	u.Backoff = []time.Duration{time.Millisecond, time.Millisecond}
	return u
}

func TestSingleUploadSendsTheFingerprint(t *testing.T) {
	f := newFake(t, 0)
	var last int64
	m, err := uploader(f).Upload(context.Background(), file(t, 10), "p1", func(s, _ int64) { last = s })
	if err != nil || m.ID != "a1" || f.got["/r2/single"] != 10 || last != 10 {
		t.Fatalf("%v %+v %v %d", err, m, f.got, last)
	}
	if f.request.SHA256 != strings.Repeat("a", 64) || f.request.ProjectID != "p1" {
		t.Errorf("request: %+v", f.request)
	}
}

func TestMultipartRetriesAPartAndCompletesInOrder(t *testing.T) {
	f := newFake(t, 3)
	f.failOnce[2] = true
	_, err := uploader(f).Upload(context.Background(), file(t, 10), "p1", nil) // parts of 4, 4, 2 bytes
	if err != nil {
		t.Fatal(err)
	}
	if f.got["/r2/part/3"] != 2 || len(f.completed) != 3 || f.completed[1].ETag != `"e2"` || f.completed[0].PartNumber != 1 {
		t.Errorf("parts %v completed %+v", f.got, f.completed)
	}
}

func TestAFailedUploadIsAbortedOnTheServer(t *testing.T) {
	f := newFake(t, 2)
	f.failAll = true
	if _, err := uploader(f).Upload(context.Background(), file(t, 8), "p1", nil); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want a 503 error, got %v", err)
	}
	if f.aborted != 1 {
		t.Errorf("aborted %d times", f.aborted)
	}
}
