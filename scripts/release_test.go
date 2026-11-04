package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTargetsRejectInvalidOrDuplicate(t *testing.T) {
	for _, input := range []string{"", "darwin", "darwin/../arm64", "plan9/amd64", "linux/386", "linux/amd64,linux/amd64"} {
		if _, err := parseTargets(input); err == nil {
			t.Errorf("accepted invalid target list %q", input)
		}
	}
	if got, err := parseTargets(defaultTargets + ",windows/arm64,windows/amd64"); err != nil || len(got) != 6 {
		t.Fatalf("six cross-compilation targets: got %v, %v", got, err)
	}
}

func TestArchivesContainRelativeNamesAndBinaryModes(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "rawr-apps_test_linux_arm64")
	if err := os.MkdirAll(filepath.Join(bundle, "LICENSES"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "ghrawr"), []byte("binary fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "LICENSES", "LICENSE"), []byte("license fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, windows := range []bool{false, true} {
		archive := filepath.Join(root, fmt.Sprintf("release-%t", windows))
		if err := writeArchive(bundle, archive, windows); err != nil {
			t.Fatal(err)
		}
		entries := map[string]os.FileMode{}
		if windows {
			reader, err := zip.OpenReader(archive)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range reader.File {
				entries[file.Name] = file.Mode().Perm()
			}
			reader.Close()
		} else {
			file, err := os.Open(archive)
			if err != nil {
				t.Fatal(err)
			}
			gzipReader, err := gzip.NewReader(file)
			if err != nil {
				t.Fatal(err)
			}
			tarReader := tar.NewReader(gzipReader)
			for {
				header, err := tarReader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				entries[header.Name] = os.FileMode(header.Mode)
			}
			gzipReader.Close()
			file.Close()
		}
		prefix := filepath.Base(bundle) + "/"
		if len(entries) != 2 || entries[prefix+"ghrawr"] != 0755 || entries[prefix+"LICENSES/LICENSE"] != 0644 {
			t.Errorf("unexpected archive contents/modes: %v", entries)
		}
		for name := range entries {
			if !strings.HasPrefix(name, prefix) || strings.Contains(name, "..") || strings.Contains(name, "\\") {
				t.Errorf("unsafe archive path %q", name)
			}
		}
	}
}

func TestChecksumsMatchBytesAndSortNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "z"), []byte("last"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeChecksums(dir, []string{"z", "a"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x  a\n%x  z\n", sha256.Sum256([]byte("first")), sha256.Sum256([]byte("last")))
	if string(data) != want {
		t.Fatalf("checksum output = %q, want %q", data, want)
	}
}

func TestPublicBundleIncludesReadmeMediaAndExcludesLocalCaptures(t *testing.T) {
	source, bundle := t.TempDir(), t.TempDir()
	want := map[string]string{
		"README.md":                     "<img src=\"assets/rawr-logo.png\" alt=\"Rawr\" width=\"220\">\n![Rawr demo](assets/demo.gif)\n![Native panes](assets/rex-resource-panes.png)",
		"docs/RAWRKIT.md":               "public SDK guide",
		"docs/USAGE.md":                 "public usage guide",
		"docs/VERIFICATION.md":          "public verification summary",
		"docs/BRANDING.md":              "public artwork provenance",
		"assets/rawr-logo.png":          "public logo fixture",
		"assets/demo.gif":               "public demo fixture",
		"assets/rex-resource-panes.png": "public native pane fixture",
		"examples/noterawr/main.go":     "package main",
	}
	write := func(name, value string) {
		t.Helper()
		path := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range want {
		write(name, value)
	}
	for _, name := range []string{"docs/evidence/session.json", "docs/evidence/screenshot.png", "docs/PRIVATE.md", "assets/private-capture.png", "examples/noterawr/local-state.json"} {
		write(name, "local-only capture; never package")
	}
	if err := copyPublicFiles(source, bundle); err != nil {
		t.Fatal(err)
	}
	count := 0
	err := filepath.WalkDir(bundle, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(bundle, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		value, allowed := want[name]
		if !allowed {
			t.Fatalf("packaged non-public file %s", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(data) != value {
			t.Errorf("bundle changed public file %s", name)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != len(want) {
		t.Fatalf("bundle copied %d public files, want %d (including README logo and demo)", count, len(want))
	}
}
