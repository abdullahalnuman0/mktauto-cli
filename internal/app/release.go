package app

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
)

// ADR-090: `mktauto release-summary`, the terminal version of the website's mktauto-release-summary.py (ADR-054). It
// runs Gradle in the developer's Android project, reads the merged manifest and the resolved release libraries, and
// writes the same "mktauto-release-summary/1" file for the app's Builds tab. Only the package name, versions, SDK
// levels, permission names and library coordinates are in it: no source code, no binary, no build secrets.
// --upload sends that file (and nothing else) to POST /api/agent/builds, which validates every field again.

const releaseFormat = "mktauto-release-summary/1"

const androidNS = "http://schemas.android.com/apk/res/android"

var (
	// Gradle project paths ("app", "apps:mobile") and variant names ("release", "prodRelease"): letters and digits only,
	// so nothing but a task name can ever reach Gradle's command line.
	moduleName  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}(:[A-Za-z0-9_-]{1,100}){0,9}$`)
	variantName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,99}$`)
	// The same rule as the server's apps.package_name.
	packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$`)
	// A line of `gradle dependencies`: "+--- group:name:version", "\--- group:name:1.0 -> 1.2 (*)".
	dependencyLine = regexp.MustCompile(`^[| ]*[+\\]--- (\S+?)(?: -> (\S+))?(?: \((?:\*|c|n)\))?\s*$`)
)

const maxManifestBytes = 16 << 20

type releasePermission struct {
	Name   string `json:"name"`
	MaxSdk *int   `json:"maxSdk"`
}

type releaseLibrary struct {
	Group   string `json:"group"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// releaseSummary: the file, field for field as mktauto-release-summary.py writes it.
type releaseSummary struct {
	Format      string              `json:"format"`
	GeneratedAt string              `json:"generatedAt"`
	Variant     string              `json:"variant"`
	PackageName string              `json:"packageName"`
	VersionCode int64               `json:"versionCode"`
	VersionName *string             `json:"versionName"`
	MinSdk      *int                `json:"minSdk"`
	TargetSdk   *int                `json:"targetSdk"`
	Permissions []releasePermission `json:"permissions"`
	Libraries   []releaseLibrary    `json:"libraries"`
}

func (e *Env) releaseSummary(ctx context.Context, args []string) error {
	fs := e.flags("release-summary", "release-summary [--dir DIR] [--module app] [--variant release] [--upload] [--json]\n\n"+
		"Summarises an Android release (version, SDK levels, permissions, libraries) for the app's Builds tab in MKTAuto,\n"+
		"which compares it with the previous release and builds a Data safety checklist. Run it in the project's root\n"+
		"folder (or pass --dir): it runs that project's ./gradlew. No source code or app file leaves this computer;\n"+
		"--upload sends only the summary file. For CI, MKTAUTO_TOKEN may be an API key (mkt_…) with \"upload release\n"+
		"summaries\".\n")
	dir := fs.String("dir", ".", "the Android project's root folder (where gradlew is)")
	module := fs.String("module", "app", "application module (Gradle path, e.g. app or apps:mobile)")
	variant := fs.String("variant", "release", "build variant (e.g. release, prodRelease)")
	pkg := fs.String("package", "", "application ID, if the merged manifest has none")
	skipBuild := fs.Bool("skip-build", false, "don't build the manifest again (dependencies are still listed)")
	output := fs.String("output", "", "output file (default mktauto-release-<versionCode>.json in this folder)")
	fs.StringVar(output, "o", "", "same as --output")
	upload := fs.Bool("upload", false, "also upload it to MKTAuto (the app must be added under Apps on the website)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return withCode(exitUsage, fmt.Errorf("unexpected %q: options start with --", fs.Arg(0)))
	}
	if !moduleName.MatchString(*module) {
		return withCode(exitUsage, fmt.Errorf("--module %q isn't a Gradle module path (letters, digits, - and _, parts separated by :)", *module))
	}
	if !variantName.MatchString(*variant) {
		return withCode(exitUsage, fmt.Errorf("--variant %q isn't a build variant name (letters and digits, e.g. release)", *variant))
	}
	if *pkg != "" && !packageName.MatchString(*pkg) {
		return withCode(exitUsage, fmt.Errorf("--package %q isn't an application ID (e.g. com.example.app)", *pkg))
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	gradlew, err := findGradlew(root)
	if err != nil {
		return err
	}
	// Signed in before the build when uploading, so a long build doesn't end in "not signed in".
	var c *api.Client
	if *upload {
		if c, err = e.uploadClient(ctx); err != nil {
			return err
		}
	}
	say := func(format string, a ...any) {
		if !*asJSON {
			e.printf(format, a...)
		}
	}

	if !*skipBuild {
		say("Building the %s manifest…\n", *variant)
		if err := buildManifest(ctx, gradlew, root, *module, *variant); err != nil {
			return err
		}
	}
	manifest, err := findMergedManifest(root, *module, *variant)
	if err != nil {
		return err
	}
	data, err := readLimited(manifest, maxManifestBytes)
	if err != nil {
		return err
	}
	s, err := parseManifest(data)
	if err != nil {
		return fmt.Errorf("can't read the merged manifest %s: %w", manifest, err)
	}
	say("Listing dependencies…\n")
	deps, err := runGradle(ctx, gradlew, root, "-q", ":"+*module+":dependencies", "--configuration", *variant+"RuntimeClasspath")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("gradle couldn't list the %sRuntimeClasspath dependencies:\n%s", *variant, tail(deps, 2000))
	}
	s.Libraries = parseDependencies(deps)
	if *pkg != "" {
		s.PackageName = *pkg
	}
	if s.PackageName == "" {
		return withCode(exitUsage, errors.New("the merged manifest has no package name: pass --package com.example.app"))
	}
	if !packageName.MatchString(s.PackageName) {
		return fmt.Errorf("the merged manifest's package name %q isn't an application ID: pass --package com.example.app", s.PackageName)
	}
	if s.VersionCode <= 0 {
		return errors.New("the merged manifest has no versionCode")
	}
	s.Format, s.Variant = releaseFormat, *variant
	s.GeneratedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")

	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := *output
	if path == "" {
		path = fmt.Sprintf("mktauto-release-%d.json", s.VersionCode)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("can't write %s: %w", path, err)
	}
	version := ""
	if s.VersionName != nil {
		version = *s.VersionName + " "
	}
	say("%s Wrote %s: %s %s(%d), %s, %s.\n", green.Render("✓"), path, s.PackageName, version, s.VersionCode,
		plural(len(s.Permissions), "permission"), libraries(len(s.Libraries)))

	var uploaded *api.BuildUpload
	if *upload {
		if uploaded, err = c.UploadReleaseSummary(ctx, json.RawMessage(body)); err != nil {
			return uploadRefused(err, s.PackageName, path)
		}
		say("%s Uploaded build %d (%s, %s). Open the app's Builds tab in MKTAuto for the comparison and the Data safety checklist.\n",
			green.Render("✓"), uploaded.VersionCode, plural(uploaded.Permissions, "permission"), libraries(uploaded.Libraries))
	} else {
		say("  Upload it on the app's Builds tab in MKTAuto, or run this again with --upload.\n")
	}
	if *asJSON {
		return e.printJSON(map[string]any{"file": path, "summary": s, "uploaded": uploaded})
	}
	return nil
}

// uploadClient: the stored terminal session (checked first), or MKTAUTO_TOKEN, which may also be an API key (mkt_…).
func (e *Env) uploadClient(ctx context.Context) (*api.Client, error) {
	t, err := e.token()
	if err != nil || t == "" {
		return nil, withCode(exitAuth, fmt.Errorf("not signed in to %s: run `mktauto login` (or set MKTAUTO_TOKEN to an API key for CI)", e.Server))
	}
	c := api.New(e.Server, t, e.Version)
	if strings.HasPrefix(t, "mkt_") {
		return c, nil // API keys can't call /api/cli/me; the upload itself says if the key is wrong
	}
	if _, err := e.me(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// uploadRefused explains the server's answer; the summary file stays, so it can be uploaded on the website instead.
func uploadRefused(err error, pkg, path string) error {
	fallback := " The summary is saved in " + path + ": you can upload it on the app's Builds tab."
	switch {
	case api.IsProblem(err, 404, ""):
		return fmt.Errorf("MKTAuto has no app %s on your account: add it under Apps on the website first.%s", pkg, fallback)
	case api.IsProblem(err, 403, "cli_not_allowed"):
		return fmt.Errorf("this MKTAuto server doesn't accept release summaries from the terminal yet.%s", fallback)
	case api.IsProblem(err, 403, "cli_project"):
		return fmt.Errorf("this terminal session is limited to some projects, and apps aren't part of a project: sign in with a login for all projects (mktauto login).%s", fallback)
	case api.IsProblem(err, 401, ""):
		return withCode(exitAuth, fmt.Errorf("the server didn't accept this sign-in (%v): run `mktauto login`, or check MKTAUTO_TOKEN.%s", err, fallback))
	}
	var p *api.Problem
	if errors.As(err, &p) {
		return fmt.Errorf("upload refused: %s.%s", strings.TrimSuffix(p.Detail, "."), fallback)
	}
	return fmt.Errorf("upload failed: %w.%s", err, fallback)
}

// ------------------------------------------------------------------ Gradle

func findGradlew(root string) (string, error) {
	name := "gradlew"
	if runtime.GOOS == "windows" {
		name = "gradlew.bat"
	}
	path := filepath.Join(root, name)
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return "", withCode(exitUsage, fmt.Errorf("no %s in %s: run this in your Android project's root folder (or pass --dir)", name, root))
	}
	return path, nil
}

func buildManifest(ctx context.Context, gradlew, root, module, variant string) error {
	v := strings.ToUpper(variant[:1]) + variant[1:]
	first := ""
	for i, task := range []string{"process" + v + "MainManifest", "process" + v + "Manifest"} {
		out, err := runGradle(ctx, gradlew, root, "-q", ":"+module+":"+task)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The older task name is only a fallback: show the first task's error unless that task didn't exist.
		if i == 0 {
			first = out
		} else if strings.Contains(out, "not found in project") && !strings.Contains(first, "not found in project") {
			out = first
		}
		if i == 1 {
			return fmt.Errorf("gradle couldn't build the %s manifest:\n%s", variant, tail(out, 2000))
		}
	}
	return nil
}

// runGradle runs the project's own wrapper (no shell) and returns its output. MKTAuto sign-ins are removed from its
// environment: build scripts and plugins never see them.
func runGradle(ctx context.Context, gradlew, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, gradlew, args...)
	cmd.Dir = root
	cmd.Env = gradleEnv(os.Environ())
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 5 * time.Second // after Ctrl+C, don't wait forever for a child that keeps the output open
	err := cmd.Run()
	return out.String(), err
}

func gradleEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(key), "MKTAUTO_TOKEN") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func libraries(n int) string {
	if n == 1 {
		return "1 library"
	}
	return fmt.Sprintf("%d libraries", n)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "…" + s[len(s)-n:]
	}
	return s
}

// findMergedManifest: the newest AndroidManifest.xml under <module>/build/intermediates in a merged_manifest* folder
// of this variant.
func findMergedManifest(root, module, variant string) (string, error) {
	base := filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(module, ":", "/")), "build", "intermediates")
	best, bestTime := "", time.Time{}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == base {
				return err
			}
			return nil // an unreadable folder is skipped
		}
		if d.IsDir() || d.Name() != "AndroidManifest.xml" || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(base, filepath.Dir(path))
		parts := strings.Split(filepath.ToSlash(rel), "/")
		merged, ofVariant := false, false
		for _, p := range parts {
			merged = merged || strings.HasPrefix(p, "merged_manifest")
			ofVariant = ofVariant || p == variant
		}
		if !merged || !ofVariant {
			return nil
		}
		if info, err := d.Info(); err == nil && (best == "" || info.ModTime().After(bestTime)) {
			best, bestTime = path, info.ModTime()
		}
		return nil
	})
	if best == "" {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		return "", fmt.Errorf("no merged manifest for %q under %s: build the release first, or check --module and --variant", variant, base)
	}
	return best, nil
}

func readLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s is larger than %d MB", path, max>>20)
	}
	return data, nil
}

// ------------------------------------------------------------------ parsing

type xmlPermission struct {
	Name   string `xml:"http://schemas.android.com/apk/res/android name,attr"`
	MaxSdk string `xml:"http://schemas.android.com/apk/res/android maxSdkVersion,attr"`
}

type xmlManifest struct {
	XMLName     xml.Name `xml:"manifest"`
	Package     string   `xml:"package,attr"`
	VersionCode string   `xml:"http://schemas.android.com/apk/res/android versionCode,attr"`
	VersionName string   `xml:"http://schemas.android.com/apk/res/android versionName,attr"`
	UsesSdk     *struct {
		Min    string `xml:"http://schemas.android.com/apk/res/android minSdkVersion,attr"`
		Target string `xml:"http://schemas.android.com/apk/res/android targetSdkVersion,attr"`
	} `xml:"uses-sdk"`
	Permissions   []xmlPermission `xml:"uses-permission"`
	Permissions23 []xmlPermission `xml:"uses-permission-sdk-23"`
}

// parseManifest reads the merged manifest (Go's XML reader never loads external entities or DTDs).
func parseManifest(data []byte) (*releaseSummary, error) {
	var m xmlManifest
	if err := xml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	num := func(v string) *int {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return nil // empty, or a preview codename such as "Baklava"
		}
		return &n
	}
	s := &releaseSummary{PackageName: strings.TrimSpace(m.Package), Permissions: []releasePermission{}, Libraries: []releaseLibrary{}}
	if code, err := strconv.ParseInt(strings.TrimSpace(m.VersionCode), 10, 64); err == nil {
		s.VersionCode = code
	}
	if m.VersionName != "" {
		name := m.VersionName
		s.VersionName = &name
	}
	if m.UsesSdk != nil {
		s.MinSdk, s.TargetSdk = num(m.UsesSdk.Min), num(m.UsesSdk.Target)
	}
	var perms []releasePermission
	for _, p := range append(m.Permissions, m.Permissions23...) {
		if p.Name != "" {
			perms = append(perms, releasePermission{Name: p.Name, MaxSdk: num(p.MaxSdk)})
		}
	}
	sort.SliceStable(perms, func(i, j int) bool { return perms[i].Name < perms[j].Name })
	seen := map[string]bool{}
	for _, p := range perms {
		if !seen[p.Name] {
			seen[p.Name] = true
			s.Permissions = append(s.Permissions, p)
		}
	}
	return s, nil
}

// parseDependencies: the resolved external libraries (group:name → version), sorted; project modules are left out.
func parseDependencies(text string) []releaseLibrary {
	libs := map[string]releaseLibrary{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		m := dependencyLine.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(m[1], "project") {
			continue
		}
		parts := strings.Split(m[1], ":")
		if len(parts) < 2 {
			continue
		}
		version := m[2]
		if version == "" && len(parts) > 2 {
			version = parts[2]
		}
		if version == "" || strings.HasPrefix(version, "{") {
			continue
		}
		libs[parts[0]+":"+parts[1]] = releaseLibrary{Group: parts[0], Name: parts[1], Version: version}
	}
	keys := make([]string, 0, len(libs))
	for k := range libs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]releaseLibrary, 0, len(keys))
	for _, k := range keys {
		out = append(out, libs[k])
	}
	return out
}
