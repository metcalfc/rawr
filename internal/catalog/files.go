package catalog

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"github.com/metcalfc/rawr/internal/model"
	"github.com/metcalfc/rawr/internal/services"
	"github.com/metcalfc/rawr/rawrkit"
	"github.com/metcalfc/rawr/rawrkit/ui"
)

// FileTerminal is an app-owned recipe for a read-only native work target. The
// stable ID reuses this resource without changing the recipe of a live view.
func FileTerminal(binaryDir, root string, request model.FileRequest) (rawrkit.TerminalSpec, error) {
	path, err := services.ResolveFilePath(root, request.Path, request.Diff)
	if err != nil {
		return rawrkit.TerminalSpec{}, err
	}
	if request.Staged && !request.Diff {
		return rawrkit.TerminalSpec{}, fmt.Errorf("staged requires a diff request")
	}
	mode := "File"
	if request.Diff {
		mode = "Diff"
		if request.Staged {
			mode = "Staged diff"
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return rawrkit.TerminalSpec{}, err
	}
	title := mode + " · " + ui.SafeText(rel)
	// Rex labels allow 128 bytes; truncate only the display, never the identity.
	for len(title) > 128 {
		r := []rune(title)
		title = string(r[:len(r)-1])
	}
	argv := []string{filepath.Join(binaryDir, "filerawr"), "--view-file", path, "--root", root}
	if request.Diff {
		argv = append(argv, "--diff")
	}
	if request.Staged {
		argv = append(argv, "--staged")
	}
	digest := sha256.Sum256([]byte(root + "\x00" + path + "\x00" + mode))
	return rawrkit.TerminalSpec{ID: fmt.Sprintf("file:%x", digest[:16]), Title: title, Argv: argv, Cwd: root, Metadata: map[string]string{"resource": "file", "path": path, "mode": mode}}, nil
}
