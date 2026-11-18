package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testManifest = `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="com.example.notes"
    android:versionCode="42" android:versionName="2.4.0">
    <uses-sdk android:minSdkVersion="26" android:targetSdkVersion="36" />
    <uses-permission android:name="android.permission.POST_NOTIFICATIONS" />
    <uses-permission android:name="android.permission.INTERNET" />
    <uses-permission android:name="android.permission.READ_EXTERNAL_STORAGE" android:maxSdkVersion="32" />
    <uses-permission-sdk-23 android:name="android.permission.INTERNET" />
    <uses-permission android:name="" />
    <application android:label="Notes" />
</manifest>`

const testDependencies = `
releaseRuntimeClasspath - Runtime classpath of '/release'.
+--- project :core
|    \--- androidx.core:core-ktx:1.13.0 -> 1.16.0
+--- com.squareup.okhttp3:okhttp:4.12.0
|    +--- com.squareup.okio:okio:3.6.0 (*)
|    \--- org.jetbrains.kotlin:kotlin-stdlib:2.0.0 -> 2.2.0 (c)
+--- com.google.firebase:firebase-bom:34.0.0
+--- com.google.firebase:firebase-messaging -> 25.0.0
+--- org.example:strict:{strictly 1.0}
\--- androidx.core:core-ktx:1.16.0 (*)
`

func TestParseManifest(t *testing.T) {
	s, err := parseManifest([]byte(testManifest))
	if err != nil {
		t.Fatal(err)
	}
	if s.PackageName != "com.example.notes" || s.VersionCode != 42 || s.VersionName == nil || *s.VersionName != "2.4.0" ||
		s.MinSdk == nil || *s.MinSdk != 26 || s.TargetSdk == nil || *s.TargetSdk != 36 {
		t.Fatalf("manifest: %+v", s)
	}
	var names []string
	for _, p := range s.Permissions {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "android.permission.INTERNET,android.permission.POST_NOTIFICATIONS,android.permission.READ_EXTERNAL_STORAGE" {
		t.Errorf("permissions (sorted, once each, no empty names): %v", names)
	}
	if p := s.Permissions[2]; p.MaxSdk == nil || *p.MaxSdk != 32 || s.Permissions[0].MaxSdk != nil {
		t.Errorf("maxSdk: %+v", s.Permissions)
	}
	// A preview codename isn't a number; a missing versionName stays null.
	s, err = parseManifest([]byte(`<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="a.b" android:versionCode="1">` +
		`<uses-sdk android:minSdkVersion="Baklava"/></manifest>`))
	if err != nil || s.MinSdk != nil || s.TargetSdk != nil || s.VersionName != nil || len(s.Permissions) != 0 {
		t.Errorf("codename: %+v %v", s, err)
	}
	// Not a manifest, and no external entities are ever loaded.
	if _, err := parseManifest([]byte(`<project/>`)); err == nil {
		t.Error("a non-manifest was accepted")
	}
	evil := `<?xml version="1.0"?><!DOCTYPE m [<!ENTITY x SYSTEM "file:///etc/passwd">]><manifest package="&x;"/>`
	if s, err := parseManifest([]byte(evil)); err == nil && strings.Contains(s.PackageName, "root") {
		t.Errorf("external entity expanded: %q", s.PackageName)
	}
}

func TestParseDependencies(t *testing.T) {
	var got []string
	for _, l := range parseDependencies(strings.ReplaceAll(testDependencies, "\n", "\r\n")) {
		got = append(got, l.Group+":"+l.Name+":"+l.Version)
	}
	want := "androidx.core:core-ktx:1.16.0,com.google.firebase:firebase-bom:34.0.0,com.google.firebase:firebase-messaging:25.0.0," +
		"com.squareup.okhttp3:okhttp:4.12.0,com.squareup.okio:okio:3.6.0,org.jetbrains.kotlin:kotlin-stdlib:2.2.0"
	if strings.Join(got, ",") != want {
		t.Errorf("libraries:\n got %s\nwant %s", strings.Join(got, ","), want)
	}
}

func TestGradleNeverSeesTheSignIn(t *testing.T) {
	env := gradleEnv([]string{"PATH=/bin", "MKTAUTO_TOKEN=mkc_secret", "mktauto_token=x", "MKTAUTO_TOKEN_STORE=file", "MKTAUTO_SERVER=https://s", "JAVA_HOME=/j"})
	if strings.Join(env, " ") != "PATH=/bin MKTAUTO_SERVER=https://s JAVA_HOME=/j" {
		t.Errorf("env: %v", env)
	}
}

// androidProject: a folder with a fake ./gradlew that writes the merged manifest, prints the dependency tree, records
// its arguments and whether it could see MKTAUTO_TOKEN.
func androidProject(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake gradlew is a shell script")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "manifest.xml"), []byte(testManifest), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "deps.txt"), []byte(testDependencies), 0o600)
	script := `#!/bin/sh
cd "$(dirname "$0")"
echo "$@" >> calls.txt
echo "token=[$MKTAUTO_TOKEN]" >> calls.txt
case "$2" in
  :app:processReleaseMainManifest)
    mkdir -p app/build/intermediates/merged_manifest/release/processReleaseMainManifest
    cp manifest.xml app/build/intermediates/merged_manifest/release/processReleaseMainManifest/AndroidManifest.xml ;;
  :app:dependencies) cat deps.txt ;;
  :app:processBrokenMainManifest) echo "e: MainActivity.kt:12 Unresolved reference" >&2; exit 1 ;;
  *) echo "Task '$2' not found in project ':app'." >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gradlew"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReleaseSummaryWritesAndUploads(t *testing.T) {
	f, dir := setup(t)
	login(t, dir)
	project := androidProject(t)
	t.Setenv("MKTAUTO_TOKEN", "") // the stored session

	// Bad option values never reach Gradle.
	for _, args := range [][]string{{"--module", "app;rm -rf /"}, {"--variant", "../release"}, {"--package", "not a package"}} {
		if code, out := runCLI(t, dir, append([]string{"release-summary", "--dir", project}, args...)...); code != exitUsage {
			t.Errorf("%v: %d %s", args, code, out)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "calls.txt")); err == nil {
		t.Fatal("gradlew ran for a bad option")
	}
	if code, out := runCLI(t, dir, "release-summary", "--dir", t.TempDir()); code != exitUsage || !strings.Contains(out, "no gradlew in") {
		t.Errorf("no gradlew: %d %s", code, out)
	}
	// A build error is shown as it is, not hidden behind the fallback task's "not found".
	if code, out := runCLI(t, dir, "release-summary", "--dir", project, "--variant", "broken"); code == 0 ||
		!strings.Contains(out, "e: MainActivity.kt:12 Unresolved reference") || strings.Contains(out, "not found in project") {
		t.Errorf("build error: %d %s", code, out)
	}
	_ = os.Remove(filepath.Join(project, "calls.txt"))

	code, out := runCLI(t, dir, "release-summary", "--dir", project, "--upload")
	if code != 0 || !strings.Contains(out, "Wrote mktauto-release-42.json: com.example.notes 2.4.0 (42), 3 permissions, 6 libraries") ||
		!strings.Contains(out, "Uploaded build 42 (3 permissions, 6 libraries)") {
		t.Fatalf("release-summary: %d %s", code, out)
	}
	// The file has exactly the website script's fields.
	data, err := os.ReadFile(filepath.Join(dir, "mktauto-release-42.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range file {
		keys = append(keys, k)
	}
	for _, k := range []string{"format", "generatedAt", "variant", "packageName", "versionCode", "versionName", "minSdk", "targetSdk", "permissions", "libraries"} {
		if _, ok := file[k]; !ok {
			t.Errorf("missing %s in %s", k, data)
		}
	}
	if len(file) != 10 || file["format"] != "mktauto-release-summary/1" || file["variant"] != "release" {
		t.Errorf("file: %v", keys)
	}
	// What was uploaded is the file, with the session.
	if len(f.builds) != 1 || f.builds[0]["versionCode"] != float64(42) || f.buildAuth[0] != "Bearer mkc_session" {
		t.Errorf("uploaded: %v %v", f.builds, f.buildAuth)
	}
	// Gradle ran the manifest and dependency tasks and never saw a token.
	calls, _ := os.ReadFile(filepath.Join(project, "calls.txt"))
	if !strings.Contains(string(calls), "-q :app:processReleaseMainManifest") ||
		!strings.Contains(string(calls), "-q :app:dependencies --configuration releaseRuntimeClasspath") || strings.Contains(string(calls), "mk") {
		t.Errorf("gradle calls: %s", calls)
	}

	// An app that isn't in MKTAuto: the file stays, and it says what to do.
	code, out = runCLI(t, dir, "release-summary", "--dir", project, "--skip-build", "--package", "com.example.other", "-o", "other.json", "--upload")
	if code == 0 || !strings.Contains(out, "no app com.example.other") || !strings.Contains(out, "saved in other.json") {
		t.Errorf("unknown app: %d %s", code, out)
	}
	// --json: the data only.
	code, out = runCLI(t, dir, "release-summary", "--dir", project, "--skip-build", "--json")
	if code != 0 || strings.Contains(out, "Wrote") || !strings.Contains(out, `"file": "mktauto-release-42.json"`) || !strings.Contains(out, `"uploaded": null`) {
		t.Errorf("--json: %d %s", code, out)
	}
}

// CI: MKTAUTO_TOKEN may be an API key (mkt_…), which reaches only the upload; Gradle doesn't get it.
func TestReleaseSummaryWithAnAPIKey(t *testing.T) {
	f, dir := setup(t)
	project := androidProject(t)
	t.Setenv("MKTAUTO_TOKEN", "mkt_key")
	code, out := runCLI(t, dir, "release-summary", "--dir", project, "--upload")
	if code != 0 || !strings.Contains(out, "Uploaded build 42") {
		t.Fatalf("api key: %d %s", code, out)
	}
	if len(f.buildAuth) != 1 || f.buildAuth[0] != "Bearer mkt_key" {
		t.Errorf("auth: %v", f.buildAuth)
	}
	for _, c := range f.calls {
		if c != "POST /api/agent/builds" {
			t.Errorf("unexpected call with an API key: %s", c)
		}
	}
	if calls, _ := os.ReadFile(filepath.Join(project, "calls.txt")); strings.Contains(string(calls), "mkt_") {
		t.Errorf("gradle saw the key: %s", calls)
	}
	// Not signed in at all: it says so before building anything.
	t.Setenv("MKTAUTO_TOKEN", "")
	_ = os.Remove(filepath.Join(project, "calls.txt"))
	if code, out := runCLI(t, dir, "release-summary", "--dir", project, "--upload"); code != exitAuth || !strings.Contains(out, "mktauto login") {
		t.Errorf("signed out: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(project, "calls.txt")); err == nil {
		t.Error("gradle ran although the upload couldn't happen")
	}
}
