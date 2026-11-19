package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
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

// testKey makes a signing key for this test and makes Fetch trust it (instead of the key built into the program).
func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := publicKey
	publicKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { publicKey = old })
	return priv
}

func sign(key ed25519.PrivateKey, data []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)) + "\n"
}

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
	key := testKey(t)
	program := []byte("#!/bin/sh\necho new version\n")
	linux := tarGz("mktauto", program)
	windows := zipOf("mktauto.exe", program)
	sums := []byte(fmt.Sprintf("%s  mktauto_1.2.0_linux_amd64.tar.gz\n%s  mktauto_1.2.0_windows_arm64.zip\n", sum(linux), sum(windows)))
	tampered := false
	apiCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/web/releases/latest": // what github.com does: redirect to the tag page
			http.Redirect(w, r, "/web/releases/tag/v1.2.0", http.StatusFound)
		case "/api/releases/latest":
			apiCalls++
			fmt.Fprint(w, `{"tag_name":"v1.2.0"}`)
		case "/dl/v1.2.0/checksums.txt":
			_, _ = w.Write(sums)
		case "/dl/v1.2.0/checksums.txt.sig":
			fmt.Fprint(w, sign(key, sums))
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
	s := &Source{Web: srv.URL + "/web", API: srv.URL + "/api", Download: srv.URL + "/dl", HTTP: srv.Client()}
	ctx := context.Background()
	if tag, err := s.Latest(ctx); err != nil || tag != "v1.2.0" || apiCalls != 0 {
		t.Fatalf("latest from the redirect, without the rate-limited API: %q %v (api calls %d)", tag, err, apiCalls)
	}
	noWeb := &Source{Web: srv.URL + "/missing", API: srv.URL + "/api", Download: srv.URL + "/dl", HTTP: srv.Client()}
	if tag, err := noWeb.Latest(ctx); err != nil || tag != "v1.2.0" || apiCalls != 1 {
		t.Fatalf("falls back to the API: %q %v", tag, err)
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

// signedRelease serves release v1.2.0 whose checksums.txt.sig is chosen by sig ("" = no signature file), plus the
// unsigned v0.11.0 and v0.9.0 releases (built before signing existed).
func signedRelease(t *testing.T, program []byte, sig func(sums []byte) string) *Source {
	t.Helper()
	archive := tarGz("mktauto", program)
	sums := []byte(sum(archive) + "  mktauto_1.2.0_linux_amd64.tar.gz\n")
	old := []byte(sum(archive) + "  mktauto_0.11.0_linux_amd64.tar.gz\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dl/v1.2.0/checksums.txt":
			_, _ = w.Write(sums)
		case "/dl/v1.2.0/checksums.txt.sig":
			if s := sig(sums); s != "" {
				fmt.Fprint(w, s)
				return
			}
			http.NotFound(w, r)
		case "/dl/v1.2.0/mktauto_1.2.0_linux_amd64.tar.gz", "/dl/v0.11.0/mktauto_0.11.0_linux_amd64.tar.gz":
			_, _ = w.Write(archive)
		case "/dl/v0.11.0/checksums.txt":
			_, _ = w.Write(old)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Source{Download: srv.URL + "/dl", HTTP: srv.Client()}
}

func TestFetchVerifiesTheReleaseSignature(t *testing.T) {
	key := testKey(t)
	program := []byte("new program")
	ctx := context.Background()

	valid := signedRelease(t, program, func(sums []byte) string { return sign(key, sums) })
	if got, err := valid.Fetch(ctx, "v1.2.0", "linux", "amd64"); err != nil || !bytes.Equal(got, program) {
		t.Fatalf("a correctly signed release installs: %v", err)
	}

	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	for name, sig := range map[string]func([]byte) string{
		"signed by another key": func(sums []byte) string { return sign(otherKey, sums) },
		"signature of other bytes": func(sums []byte) string {
			return sign(key, append(append([]byte{}, sums...), []byte("evil  mktauto_1.2.0_linux_amd64.tar.gz\n")...))
		},
		"not base64":   func([]byte) string { return "not a signature!" },
		"wrong length": func([]byte) string { return base64.StdEncoding.EncodeToString([]byte("short")) },
	} {
		src := signedRelease(t, program, sig)
		if _, err := src.Fetch(ctx, "v1.2.0", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "signature doesn't match") {
			t.Errorf("%s: must be refused, got %v", name, err)
		}
	}

	missing := signedRelease(t, program, func([]byte) string { return "" })
	if _, err := missing.Fetch(ctx, "v1.2.0", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "has no signature") {
		t.Errorf("a release without checksums.txt.sig must be refused: %v", err)
	}

	for _, tag := range []string{"v0.11.0", "v0.9.0", "v0.11.0-beta.1"} {
		if _, err := valid.Fetch(ctx, tag, "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "before releases were signed") {
			t.Errorf("%s has no signature and must be refused (also with --version): %v", tag, err)
		}
	}
	if !Signed("v0.11.1") || !Signed("v0.12.0-beta.1") || Signed("v0.11.0") {
		t.Error("signed releases are the ones after v0.11.0")
	}
}

func TestFetchFailsClosedWithoutTheReleaseKey(t *testing.T) {
	key := testKey(t)
	src := signedRelease(t, []byte("p"), func(sums []byte) string { return sign(key, sums) })
	for _, k := range []string{placeholderKey, "", "bm90IGEga2V5"} {
		publicKey = k
		if _, err := src.Fetch(context.Background(), "v1.2.0", "linux", "amd64"); !errors.Is(err, ErrNoSigningKey) ||
			!strings.Contains(err.Error(), "this build has no release signing key") {
			t.Errorf("key %q: must refuse, got %v", k, err)
		}
	}
	if releasePublicKey == placeholderKey && EmbeddedPublicKey() != "" {
		t.Error("the placeholder is not a key")
	}
}
