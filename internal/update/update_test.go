package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tarGz(name string, data []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hi"))
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(data)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipOf(name string, data []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	_, _ = w.Write(data)
	zw.Close()
	return buf.Bytes()
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestVersionsCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v1.10.0", "1.9.3", true}, {"v1.0.0", "v1.0.0", false}, {"1.0.0", "1.0.0-beta.1", true}, {"1.0.0-beta.1", "1.0.0", false}, {"0.2.0", "0.10.0", false}} {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("Newer(%s, %s) != %v", c.a, c.b, c.want)
		}
	}
	if AssetName("v1.2.0", "windows", "arm64") != "mktauto_1.2.0_windows_arm64.zip" || AssetName("1.2.0", "linux", "amd64") != "mktauto_1.2.0_linux_amd64.tar.gz" {
		t.Error("asset names must match the release workflow and the website")
	}
}

func TestFetchChecksTheDownloadAndUpdatesTheProgram(t *testing.T) {
	program := []byte("#!/bin/sh\necho new version\n")
	linux := tarGz("mktauto", program)
	windows := zipOf("mktauto.exe", program)
	tampered := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/releases/latest":
			fmt.Fprint(w, `{"tag_name":"v1.2.0"}`)
		case "/dl/v1.2.0/checksums.txt":
			fmt.Fprintf(w, "%s  mktauto_1.2.0_linux_amd64.tar.gz\n%s  mktauto_1.2.0_windows_arm64.zip\n", sum(linux), sum(windows))
		case "/dl/v1.2.0/mktauto_1.2.0_linux_amd64.tar.gz":
			if tampered {
				_, _ = w.Write(append(linux, 0))
				return
			}
			_, _ = w.Write(linux)
		case "/dl/v1.2.0/mktauto_1.2.0_windows_arm64.zip":
			_, _ = w.Write(windows)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := &Source{API: srv.URL + "/api", Download: srv.URL + "/dl", HTTP: srv.Client()}
	ctx := context.Background()
	if tag, err := s.Latest(ctx); err != nil || tag != "v1.2.0" {
		t.Fatalf("latest: %q %v", tag, err)
	}
	got, err := s.Fetch(ctx, "v1.2.0", "linux", "amd64")
	if err != nil || !bytes.Equal(got, program) {
		t.Fatalf("linux: %v", err)
	}
	if got, err := s.Fetch(ctx, "v1.2.0", "windows", "arm64"); err != nil || !bytes.Equal(got, program) {
		t.Fatalf("windows: %v", err)
	}
	if _, err := s.Fetch(ctx, "v1.2.0", "darwin", "arm64"); err == nil || !strings.Contains(err.Error(), "isn't in this release") {
		t.Errorf("missing build: %v", err)
	}
	tampered = true
	if _, err := s.Fetch(ctx, "v1.2.0", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("a file that doesn't match its checksum must be refused: %v", err)
	}

	exe := filepath.Join(t.TempDir(), "mktauto")
	_ = os.WriteFile(exe, []byte("old"), 0o755)
	if err := Replace(exe, program); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(exe)
	st, _ := os.Stat(exe)
	if !bytes.Equal(data, program) || (runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0) {
		t.Errorf("replaced program: %q %v", data, st.Mode())
	}
}
