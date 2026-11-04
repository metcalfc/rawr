// release builds distributable bundles using the Go standard library only.
// Run from the repository root: go run ./scripts/release.go -version v0.1.0
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const defaultTargets = "darwin/arm64,darwin/amd64,linux/arm64,linux/amd64"

var apps = []string{"ghrawr", "filerawr", "exerawr", "rawr"}
var safeVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Keep local acceptance captures and other ignored files out of distributable
// bundles, including when a release is built from a developer's full checkout.
var publicFiles = []string{
	"README.md",
	"docs/RAWRKIT.md",
	"docs/USAGE.md",
	"docs/VERIFICATION.md",
	"docs/BRANDING.md",
	"assets/rawr-logo.png",
	"assets/demo.gif",
	"examples/noterawr/main.go",
}

type target struct{ OS, Arch string }
type module struct {
	Path, Version, Dir string
	Main               bool
	Replace            *module
}
type licenseRecord struct {
	Module  string   `json:"module"`
	Version string   `json:"version"`
	Files   []string `json:"files"`
}

func main() {
	version := flag.String("version", "dev", "release version used in bundle names")
	commit := flag.String("commit", "unknown", "source commit recorded in binaries and build metadata")
	targetsFlag := flag.String("targets", defaultTargets, "comma-separated GOOS/GOARCH targets")
	out := flag.String("out", "dist", "output directory")
	flag.Parse()
	if err := release(*version, *commit, *targetsFlag, *out); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func release(version, commit, targetList, out string) error {
	if !safeVersion.MatchString(version) {
		return errors.New("version must contain only letters, digits, dots, underscores and hyphens, beginning with a letter or digit")
	}
	if !safeVersion.MatchString(commit) {
		return errors.New("commit must contain only letters, digits, dots, underscores and hyphens, beginning with a letter or digit")
	}
	targets, err := parseTargets(targetList)
	if err != nil {
		return err
	}
	if _, err := os.Stat("go.mod"); err != nil {
		return errors.New("run the release builder from the repository root")
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(out, ".release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := run(nil, "go", "mod", "download", "all"); err != nil {
		return err
	}
	licenses := filepath.Join(stage, "licenses")
	if err := collectLicenses(licenses); err != nil {
		return err
	}
	var archives []string
	for _, t := range targets {
		name := fmt.Sprintf("rawr-apps_%s_%s_%s", version, t.OS, t.Arch)
		bundle := filepath.Join(stage, name)
		if err := os.MkdirAll(bundle, 0755); err != nil {
			return err
		}
		for _, app := range apps {
			binary := app
			if t.OS == "windows" {
				binary += ".exe"
			}
			fmt.Printf("Building %s for %s/%s\n", app, t.OS, t.Arch)
			linkFlags := "-X github.com/metcalfc/rawr/internal/buildinfo.Version=" + version + " -X github.com/metcalfc/rawr/internal/buildinfo.Commit=" + commit
			if err := run([]string{"CGO_ENABLED=0", "GOOS=" + t.OS, "GOARCH=" + t.Arch}, "go", "build", "-trimpath", "-buildvcs=false", "-ldflags", linkFlags, "-o", filepath.Join(bundle, binary), "./cmd/"+app); err != nil {
				return err
			}
		}
		if err := copyPublicFiles(".", bundle); err != nil {
			return err
		}
		// A project license is included if present; the builder does not choose one.
		for _, name := range []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "LICENCE", "COPYING", "NOTICE"} {
			if _, err := os.Stat(name); err == nil {
				if err := copyFile(name, filepath.Join(bundle, name)); err != nil {
					return err
				}
			}
		}
		if err := copyTree(licenses, filepath.Join(bundle, "LICENSES")); err != nil {
			return err
		}
		info := map[string]any{"version": version, "commit": commit, "go_version": runtime.Version(), "goos": t.OS, "goarch": t.Arch, "cgo_enabled": false, "apps": apps, "sdk_module": "github.com/metcalfc/rawr", "sdk_package": "github.com/metcalfc/rawr/rawrkit", "rex_runtime_acceptance": "See docs/RAWRKIT.md; cross-compilation does not verify Rex integration."}
		data, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(bundle, "BUILD-INFO.json"), append(data, '\n'), 0644); err != nil {
			return err
		}
		if err := writeChecksums(bundle, appsForTarget(t)); err != nil {
			return err
		}
		ext := ".tar.gz"
		if t.OS == "windows" {
			ext = ".zip"
		}
		archive := name + ext
		temporary := filepath.Join(stage, archive)
		if err := writeArchive(bundle, temporary, t.OS == "windows"); err != nil {
			return err
		}
		if err := os.Rename(temporary, filepath.Join(out, archive)); err != nil {
			return err
		}
		archives = append(archives, archive)
		fmt.Println("Created", filepath.Join(out, archive))
	}
	return writeChecksums(out, archives)
}

func parseTargets(list string) ([]target, error) {
	var result []target
	seen := map[string]bool{}
	for _, value := range strings.Split(list, ",") {
		parts := strings.Split(strings.TrimSpace(value), "/")
		if len(parts) != 2 || (parts[0] != "darwin" && parts[0] != "linux" && parts[0] != "windows") || (parts[1] != "arm64" && parts[1] != "amd64") {
			return nil, fmt.Errorf("unsupported target %q; choose darwin, linux or windows with arm64 or amd64", value)
		}
		key := strings.Join(parts, "/")
		if seen[key] {
			return nil, fmt.Errorf("duplicate target %q", key)
		}
		seen[key] = true
		result = append(result, target{parts[0], parts[1]})
	}
	return result, nil
}

func appsForTarget(t target) []string {
	result := append([]string(nil), apps...)
	if t.OS == "windows" {
		for i := range result {
			result[i] += ".exe"
		}
	}
	return result
}

func run(env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func collectLicenses(dest string) error {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("list dependency modules: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	var records []licenseRecord
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	for {
		var m module
		if err := decoder.Decode(&m); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		if m.Main {
			continue
		}
		source := m
		if m.Replace != nil {
			source = *m.Replace
		}
		if source.Dir == "" {
			return fmt.Errorf("module %s has no downloaded source directory", m.Path)
		}
		entries, err := os.ReadDir(source.Dir)
		if err != nil {
			return err
		}
		folder := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(m.Path + "@" + m.Version)
		record := licenseRecord{Module: m.Path, Version: m.Version}
		foundLicense := false
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToUpper(entry.Name())
			license := strings.HasPrefix(name, "LICENSE") || strings.HasPrefix(name, "LICENCE") || strings.HasPrefix(name, "COPYING")
			if !license && !strings.HasPrefix(name, "NOTICE") && name != "PATENTS" && name != "AUTHORS" {
				continue
			}
			foundLicense = foundLicense || license
			relative := filepath.Join(folder, entry.Name())
			if err := copyFile(filepath.Join(source.Dir, entry.Name()), filepath.Join(dest, relative)); err != nil {
				return err
			}
			record.Files = append(record.Files, filepath.ToSlash(relative))
		}
		if !foundLicense {
			return fmt.Errorf("module %s@%s has no recognized root license file; inspect before distributing", m.Path, m.Version)
		}
		records = append(records, record)
	}
	goLicense := ""
	for _, path := range []string{filepath.Join(runtime.GOROOT(), "LICENSE"), filepath.Join(filepath.Dir(runtime.GOROOT()), "LICENSE")} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			goLicense = path
			break
		}
	}
	if goLicense == "" {
		return fmt.Errorf("Go runtime LICENSE not found in GOROOT or its packaging directory")
	}
	if err := copyFile(goLicense, filepath.Join(dest, "go-runtime", "LICENSE")); err != nil {
		return err
	}
	goFiles := []string{"go-runtime/LICENSE"}
	if path := filepath.Join(runtime.GOROOT(), "PATENTS"); fileExists(path) {
		if err := copyFile(path, filepath.Join(dest, "go-runtime", "PATENTS")); err != nil {
			return err
		}
		goFiles = append(goFiles, "go-runtime/PATENTS")
	}
	records = append(records, licenseRecord{Module: "Go runtime and standard library", Version: runtime.Version(), Files: goFiles})
	sort.Slice(records, func(i, j int) bool { return records[i].Module < records[j].Module })
	data, err = json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "MANIFEST.json"), append(data, '\n'), 0644)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func copyPublicFiles(source, dest string) error {
	for _, name := range publicFiles {
		if err := copyFile(filepath.Join(source, filepath.FromSlash(name)), filepath.Join(dest, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("copy public bundle file %s: %w", name, err)
		}
	}
	return nil
}

func copyFile(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

func copyTree(source, dest string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(dest, relative))
	})
}

func writeChecksums(dir string, names []string) error {
	names = append([]string(nil), names...)
	sort.Strings(names)
	var output strings.Builder
	for _, name := range names {
		file, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		if err := errors.Join(copyErr, file.Close()); err != nil {
			return err
		}
		fmt.Fprintf(&output, "%s  %s\n", hex.EncodeToString(hash.Sum(nil)), name)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(output.String()), 0644)
}

func writeArchive(bundle, destination string, windows bool) (result error) {
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	var zipWriter *zip.Writer
	var tarWriter *tar.Writer
	if windows {
		zipWriter = zip.NewWriter(file)
		defer func() { result = errors.Join(result, zipWriter.Close()) }()
	} else {
		gzipWriter := gzip.NewWriter(file)
		defer func() { result = errors.Join(result, gzipWriter.Close()) }()
		tarWriter = tar.NewWriter(gzipWriter)
		defer func() { result = errors.Join(result, tarWriter.Close()) }()
	}
	return filepath.WalkDir(bundle, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular archive entry %s", path)
		}
		relative, err := filepath.Rel(filepath.Dir(bundle), path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		var output io.Writer
		if windows {
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name, header.Method = name, zip.Deflate
			header.Modified = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
			output, err = zipWriter.CreateHeader(header)
			if err != nil {
				return err
			}
		} else {
			header := &tar.Header{Name: name, Mode: int64(info.Mode().Perm()), Size: info.Size(), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}
			if err := tarWriter.WriteHeader(header); err != nil {
				return err
			}
			output = tarWriter
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		return errors.Join(copyErr, input.Close())
	})
}
