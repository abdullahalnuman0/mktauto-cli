package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileStoreIsPrivateAndPerServer(t *testing.T) {
	t.Setenv("MKTAUTO_CONFIG_DIR", t.TempDir())
	t.Setenv("MKTAUTO_TOKEN_STORE", "file")
	s := NewTokenStore()
	if _, err := s.Get("https://a"); err != ErrNoToken {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
	if err := s.Set("https://a", "mkc_one"); err != nil {
		t.Fatal(err)
	}
	_ = s.Set("https://b", "mkc_two")
	if v, _ := s.Get("https://a"); v != "mkc_one" {
		t.Errorf("got %q", v)
	}
	dir, _ := Dir()
	st, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("credentials must be readable only by the user: %v", st.Mode().Perm())
	}
	_ = s.Delete("https://a")
	if _, err := s.Get("https://a"); err != ErrNoToken {
		t.Error("deleted")
	}
	if v, _ := s.Get("https://b"); v != "mkc_two" {
		t.Error("other server kept")
	}
}

func TestConfigRemembersServerAndProjects(t *testing.T) {
	t.Setenv("MKTAUTO_CONFIG_DIR", t.TempDir())
	t.Setenv("MKTAUTO_SERVER", "")
	c, _ := LoadConfig()
	if c.ServerURL("") != DefaultServer {
		t.Error("default server")
	}
	c.Server, c.LastProject["/x"] = "https://my.example", "p1"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, _ := LoadConfig()
	if c2.ServerURL("") != "https://my.example" || c2.LastProject["/x"] != "p1" || c2.ServerURL("https://flag") != "https://flag" {
		t.Errorf("%+v", c2)
	}
}
