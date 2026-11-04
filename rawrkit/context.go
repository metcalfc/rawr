package rawrkit

import "github.com/metcalfc/rawr/internal/storage"

// ReadContext reads the explicit --context-file passed by a native app launch.
// It rejects unknown fields, trailing data and oversized documents. Callers
// should not treat arbitrary project files as implicit launch context.
func ReadContext(path string) (Context, error) {
	var c Context
	err := storage.ReadJSON(path, &c)
	return c, err
}
