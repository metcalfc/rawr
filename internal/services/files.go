package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const FilePreviewBytes = 128 << 10
const FilePreviewLines = 2000
const FileListLimit = 10000

type FileChange struct {
	Path         string
	OriginalPath string
	Status       string
	Staged       bool
	Untracked    bool
}
type FileDocument struct {
	Path      string
	Lines     []string
	Truncated bool
	Binary    bool
}

func fileRoot(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("project root is not a directory")
	}
	return root, nil
}
func relativeFile(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("selection points outside the project root")
	}
	if rel == "." || strings.ContainsRune(rel, 0) {
		return "", errors.New("select a file inside the project root")
	}
	return rel, nil
}

// ResolveFilePath confines both existing paths and deleted Git paths. For a
// deletion, every existing ancestor is still resolved before the path is used.
// allowMissing is the Git-diff mode: preserve a contained symlink's Git path
// after validating its target, rather than diffing the target under another name.
func ResolveFilePath(root, path string, allowMissing bool) (string, error) {
	root, err := fileRoot(root)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	// Canonicalize parent aliases such as macOS /var -> /private/var while
	// retaining the leaf name used by Git for a contained symlink.
	{
		parent := filepath.Dir(path)
		suffix := []string{filepath.Base(path)}
		for {
			resolved, parentErr := filepath.EvalSymlinks(parent)
			if parentErr == nil {
				for i := len(suffix) - 1; i >= 0; i-- {
					resolved = filepath.Join(resolved, suffix[i])
				}
				path = resolved
				break
			}
			if !errors.Is(parentErr, os.ErrNotExist) || parent == filepath.Dir(parent) {
				break
			}
			suffix = append(suffix, filepath.Base(parent))
			parent = filepath.Dir(parent)
		}
	}
	rel, err := relativeFile(root, path)
	if err != nil {
		return "", err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer r.Close()
	info, err := r.Stat(rel)
	if err == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("select a regular file")
		}
		actual, err := filepath.EvalSymlinks(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		if _, err = relativeFile(root, actual); err != nil {
			return "", err
		}
		if allowMissing {
			return filepath.Join(root, rel), nil
		}
		return actual, nil
	}
	if !allowMissing || !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("selection is outside the project root or cannot be read: %w", err)
	}
	parent := filepath.Dir(rel)
	for {
		_, err = r.Stat(parent)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("selected path escapes or cannot be read inside project root: %w", err)
		}
		if parent == "." {
			return "", err
		}
		parent = filepath.Dir(parent)
	}
	return filepath.Join(root, rel), nil
}

func FileChanges(ctx context.Context, root string) ([]FileChange, error) {
	root, err := fileRoot(root)
	if err != nil {
		return nil, err
	}
	topResult, err := run(ctx, []string{"git", "-C", root, "rev-parse", "--show-toplevel"}, root, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if topResult.code != 0 {
		return nil, errors.New("Changes needs a Git worktree; Tree and filename filtering remain available")
	}
	top, err := fileRoot(strings.TrimSuffix(topResult.stdout, "\n"))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve Git worktree root: %w", err)
	}
	r, err := run(ctx, []string{"git", "--literal-pathspecs", "--no-optional-locks", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", "."}, root, 5*time.Second)
	if err != nil {
		return nil, err
	}
	if r.code != 0 {
		return nil, errors.New("Changes needs a Git worktree; Tree and filename filtering remain available")
	}
	rows, err := parseFileChanges(r.stdout)
	if err != nil {
		return nil, err
	}
	anchored := func(path string) (string, error) {
		path = filepath.FromSlash(path)
		if filepath.IsAbs(path) {
			return "", errors.New("Git returned an absolute change path")
		}
		if _, err := relativeFile(top, filepath.Join(top, path)); err != nil {
			return "", errors.New("Git returned a change outside its worktree")
		}
		rel, err := relativeFile(root, filepath.Join(top, path))
		if err != nil {
			return "", errors.New("Git returned a change outside the anchored project root")
		}
		return rel, nil
	}
	for i := range rows {
		rows[i].Path, err = anchored(rows[i].Path)
		if err != nil {
			return nil, err
		}
		if rows[i].OriginalPath != "" {
			rows[i].OriginalPath, err = anchored(rows[i].OriginalPath)
			if err != nil {
				return nil, err
			}
		}
	}
	return rows, nil
}
func parseFileChanges(output string) ([]FileChange, error) {
	parts := strings.Split(output, "\x00")
	rows := []FileChange{}
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if entry == "" {
			continue
		}
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, errors.New("Git returned malformed file status")
		}
		xy, path := entry[:2], entry[3:]
		if filepath.IsAbs(path) || path == "" {
			return nil, errors.New("Git returned an invalid relative path")
		}
		original := ""
		if strings.ContainsAny(xy, "RC") {
			i++
			if i >= len(parts) || parts[i] == "" {
				return nil, errors.New("Git returned an incomplete rename status")
			}
			original = parts[i]
		}
		if xy == "??" {
			rows = append(rows, FileChange{Path: path, Status: "?", Untracked: true})
		} else {
			if xy[0] != ' ' && xy[0] != '?' {
				rows = append(rows, FileChange{Path: path, OriginalPath: original, Status: string(xy[0]), Staged: true})
			}
			if xy[1] != ' ' && xy[1] != '?' {
				rows = append(rows, FileChange{Path: path, OriginalPath: original, Status: string(xy[1])})
			}
		}
		if len(rows) > FileListLimit {
			return nil, errors.New("Changes is limited to 10,000 rows; narrow this worktree")
		}
	}
	return rows, nil
}

// FileDirectory bounds a tree expansion before sorting, and opens through the
// root handle so a replaced directory cannot redirect enumeration outside it.
func FileDirectory(root, path string, limit int) ([]os.DirEntry, bool, error) {
	root, err := fileRoot(root)
	if err != nil {
		return nil, false, err
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, false, err
	}
	rel, err := filepath.Rel(root, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, false, errors.New("directory points outside the project root")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, false, err
	}
	defer r.Close()
	f, err := openRootFile(r, rel)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, errors.New("selected directory changed; refresh")
	}
	limit = max(1, min(limit, FileListLimit))
	entries, err := f.ReadDir(limit + 1)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	limited := len(entries) > limit
	if limited {
		entries = entries[:limit]
	}
	return entries, limited, nil
}

// FilenameFiles indexes names only, never file contents. Symlinks and .git are
// not traversed. Cancellation, row count and elapsed time bound the walk.
func FilenameFiles(ctx context.Context, root string) ([]string, bool, error) {
	root, err := fileRoot(root)
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	paths := []string{}
	truncated := false
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, false, err
	}
	defer r.Close()
	visited := 0
	var walk func(string, int) error
	walk = func(directory string, depth int) error {
		if truncated {
			return nil
		}
		if ctx.Err() != nil || depth > 100 {
			truncated = true
			return nil
		}
		f, err := openRootFile(r, directory)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("filename index directory changed; refresh")
		}
		for {
			entries, readErr := f.ReadDir(256)
			for _, entry := range entries {
				visited++
				if ctx.Err() != nil || visited > 5*FileListLimit {
					truncated = true
					return nil
				}
				rel := filepath.Join(directory, entry.Name())
				info, err := r.Lstat(rel)
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 {
					continue
				}
				if info.IsDir() {
					if entry.Name() != ".git" {
						if err = walk(rel, depth+1); err != nil {
							return err
						}
					}
				} else if info.Mode().IsRegular() {
					paths = append(paths, rel)
					if len(paths) >= FileListLimit {
						truncated = true
						return nil
					}
				}
				if truncated {
					return nil
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	err = walk(".", 0)
	sort.Strings(paths)
	return paths, truncated, err
}

func ReadFileDocument(ctx context.Context, root, path string, diff, staged bool) (FileDocument, error) {
	root, err := fileRoot(root)
	if err != nil {
		return FileDocument{}, err
	}
	actual, err := ResolveFilePath(root, path, diff)
	if err != nil {
		return FileDocument{}, err
	}
	doc := FileDocument{Path: actual}
	var data []byte
	if diff {
		rel, err := relativeFile(root, actual)
		if err != nil {
			return doc, err
		}
		argv := []string{"git", "--literal-pathspecs", "--no-pager", "-C", root, "diff", "--no-ext-diff", "--no-textconv", "--color=never"}
		if staged {
			argv = append(argv, "--cached")
		}
		argv = append(argv, "--", rel)
		r, err := run(ctx, argv, root, 5*time.Second)
		if err != nil {
			return doc, err
		}
		if r.code != 0 {
			return doc, errors.New("cannot read this Git diff; refresh Changes and select a tracked file")
		}
		data = []byte(r.stdout)
		if len(data) == 0 {
			doc.Lines = []string{"(No changes in this diff. Untracked files have no Git diff.)"}
			return doc, nil
		}
	} else {
		rel, err := relativeFile(root, actual)
		if err != nil {
			return doc, err
		}
		r, err := os.OpenRoot(root)
		if err != nil {
			return doc, err
		}
		defer r.Close()
		info, err := r.Stat(rel)
		if err != nil {
			return doc, err
		}
		if !info.Mode().IsRegular() {
			return doc, errors.New("select a regular file")
		}
		f, err := openRootFile(r, rel)
		if err != nil {
			return doc, err
		}
		defer f.Close()
		info, err = f.Stat()
		if err != nil {
			return doc, err
		}
		if !info.Mode().IsRegular() {
			return doc, errors.New("select a regular file")
		}
		data, err = io.ReadAll(io.LimitReader(f, FilePreviewBytes+1))
		if err != nil {
			return doc, err
		}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		doc.Binary = true
		doc.Lines = []string{"Binary file — use o to open it with the system application."}
		return doc, nil
	}
	if len(data) > FilePreviewBytes {
		data = data[:FilePreviewBytes]
		doc.Truncated = true
	}
	doc.Lines = strings.Split(strings.ReplaceAll(strings.ToValidUTF8(string(data), "�"), "\r\n", "\n"), "\n")
	if len(doc.Lines) > FilePreviewLines {
		doc.Lines = doc.Lines[:FilePreviewLines]
		doc.Truncated = true
	}
	if len(data) == 0 {
		doc.Lines = []string{"(empty file)"}
	}
	if doc.Truncated {
		doc.Lines = append(doc.Lines, "… Preview truncated; use o to open the complete file.")
	}
	return doc, nil
}
