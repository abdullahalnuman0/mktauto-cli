// Command sign signs a release's checksums.txt for the release workflow (never shipped in the release archives).
//
//	MKTAUTO_SIGNING_KEY=… go run ./internal/tools/sign dist/checksums.txt   # writes dist/checksums.txt.sig
//	MKTAUTO_SIGNING_KEY=… go run ./internal/tools/sign -public              # prints the public key for internal/update
//
// MKTAUTO_SIGNING_KEY is base64 of the 64-byte Ed25519 private key (seed followed by public key, as Go's
// ed25519.PrivateKey). It is read from the environment only, never from the command line, and never printed. The
// signature file is base64 of the raw 64-byte Ed25519 signature over the exact bytes of the file.
package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/abdullahalnuman0/mktauto-cli/internal/update"
)

func main() {
	public := flag.Bool("public", false, "print the public key of MKTAUTO_SIGNING_KEY (base64) and exit")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: MKTAUTO_SIGNING_KEY=… sign FILE   (writes FILE.sig)\n       MKTAUTO_SIGNING_KEY=… sign -public")
		flag.PrintDefaults()
	}
	flag.Parse()
	if err := run(*public, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
}

func run(public bool, args []string) error {
	key, err := parseKey(os.Getenv("MKTAUTO_SIGNING_KEY"))
	if err != nil {
		return err
	}
	pub := key.Public().(ed25519.PublicKey)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	if public {
		fmt.Println(pubB64)
		return nil
	}
	if len(args) != 1 {
		return errors.New("give the file to sign, e.g. dist/checksums.txt")
	}
	// A release signed with a key the program doesn't carry could never be installed by `mktauto update`.
	switch embedded := update.EmbeddedPublicKey(); {
	case embedded == "":
		return errors.New("internal/update still has the placeholder release public key (REPLACE_WITH_PUBLIC_KEY): put the real one in before releasing")
	case embedded != pubB64:
		return errors.New("MKTAUTO_SIGNING_KEY doesn't belong to the public key built into internal/update")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	sig := Sign(key, data)
	if !update.VerifySignature(pub, data, sig) {
		return errors.New("the new signature doesn't verify")
	}
	if err := os.WriteFile(args[0]+".sig", sig, 0o644); err != nil {
		return err
	}
	fmt.Printf("Signed %s -> %s.sig (public key %s)\n", args[0], args[0], pubB64)
	return nil
}

// parseKey reads base64 of a 64-byte Ed25519 private key and checks its public half matches its seed.
func parseKey(b64 string) (ed25519.PrivateKey, error) {
	if b64 == "" {
		return nil, errors.New("MKTAUTO_SIGNING_KEY is not set (the repository secret with the release signing key)")
	}
	raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace([]byte(b64))))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("MKTAUTO_SIGNING_KEY must be base64 of a %d-byte Ed25519 private key", ed25519.PrivateKeySize)
	}
	key := ed25519.PrivateKey(raw)
	if !bytes.Equal(ed25519.NewKeyFromSeed(key.Seed()), key) {
		return nil, errors.New("MKTAUTO_SIGNING_KEY is damaged: its public half doesn't match its seed")
	}
	return key, nil
}

// Sign returns base64 of the Ed25519 signature over data, followed by a newline.
func Sign(key ed25519.PrivateKey, data []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)) + "\n")
}
