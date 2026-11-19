package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abdullahalnuman0/mktauto-cli/internal/update"
)

func TestSignatureVerifiesWithTheUpdater(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key, err := parseKey(base64.StdEncoding.EncodeToString(priv) + "\n")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("abc  mktauto_1.2.0_linux_amd64.tar.gz\n")
	sig := Sign(key, data)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		t.Fatalf("the .sig is base64 of the raw 64-byte signature: %q", sig)
	}
	if !update.VerifySignature(pub, data, sig) || update.VerifySignature(pub, append(data, 'x'), sig) {
		t.Error("the updater must accept exactly the signed bytes")
	}
}

func TestBadKeysAreRefused(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	damaged := append(ed25519.PrivateKey{}, priv...)
	damaged[63] ^= 1
	for name, k := range map[string]string{
		"missing":      "",
		"not base64":   "%%%",
		"seed only":    base64.StdEncoding.EncodeToString(priv.Seed()),
		"damaged half": base64.StdEncoding.EncodeToString(damaged),
	} {
		if _, err := parseKey(k); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	t.Setenv("MKTAUTO_SIGNING_KEY", "")
	if err := run(false, []string{"x"}); err == nil || !strings.Contains(err.Error(), "MKTAUTO_SIGNING_KEY is not set") {
		t.Errorf("the release must fail without the secret: %v", err)
	}
}

func TestRunRefusesAKeyTheProgramDoesNotCarry(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("MKTAUTO_SIGNING_KEY", base64.StdEncoding.EncodeToString(priv))
	file := filepath.Join(t.TempDir(), "checksums.txt")
	_ = os.WriteFile(file, []byte("x\n"), 0o644)
	if err := run(false, []string{file}); err == nil {
		t.Fatal("a key that doesn't match the built-in public key (or the placeholder) must be refused")
	}
	if _, err := os.Stat(file + ".sig"); err == nil {
		t.Error("no signature may be written")
	}
}
