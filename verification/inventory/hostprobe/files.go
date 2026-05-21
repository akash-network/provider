package hostprobe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrUnavailable = errors.New("inventory source unavailable")

type FileSource struct {
	root string
}

func NewFileSource(root string) FileSource {
	if root == "" {
		root = "/"
	}

	return FileSource{root: filepath.Clean(root)}
}

func (fs FileSource) ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(fs.rooted(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return nil, ErrUnavailable
		}

		return nil, err
	}

	return data, nil
}

func (fs FileSource) ReadString(path string) (string, error) {
	data, err := fs.ReadFile(path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

func (fs FileSource) Glob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(fs.rooted(pattern))
	if err != nil {
		return nil, err
	}

	if len(matches) == 0 {
		return nil, ErrUnavailable
	}

	root := fs.root
	res := make([]string, 0, len(matches))
	for _, match := range matches {
		rel, err := filepath.Rel(root, match)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}

		res = append(res, "/"+filepath.ToSlash(rel))
	}

	if len(res) == 0 {
		return nil, ErrUnavailable
	}

	return res, nil
}

func (fs FileSource) rooted(path string) string {
	path = filepath.Clean(filepath.FromSlash(path))
	path = strings.TrimPrefix(path, string(filepath.Separator))

	return filepath.Join(fs.root, path)
}
