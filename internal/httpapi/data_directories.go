package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxDataDirectories    = 10_000
	maxDataDirectoryBytes = 1 << 20
	maxDataDirectoryDepth = 64
)

type DataDirectoriesResponse struct {
	Directories []string `json:"directories"`
}

type DataDirectoriesHandler struct {
	dataDir string
}

func NewDataDirectoriesHandler(dataDir string) *DataDirectoriesHandler {
	return &DataDirectoriesHandler{dataDir: dataDir}
}

func (handler *DataDirectoriesHandler) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	directories, err := listDataDirectories(handler.dataDir, maxDataDirectories)
	if err != nil {
		writeJSONResponse(writer, http.StatusInternalServerError, map[string]string{"error": "list data directories: " + err.Error()})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(DataDirectoriesResponse{Directories: directories}); err != nil {
		return
	}
}

func listDataDirectories(dataDir string, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("positive directory limit is required")
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directories := make([]string, 0)
	totalBytes := 0
	var walk func(string, int) error
	walk = func(directory string, depth int) error {
		if depth > maxDataDirectoryDepth {
			return fmt.Errorf("directory depth exceeds %d", maxDataDirectoryDepth)
		}
		file, err := root.Open(directory)
		if err != nil {
			return err
		}
		defer file.Close()
		for {
			entries, readErr := file.ReadDir(256)
			if readErr != nil && readErr != io.EOF {
				return readErr
			}
			for _, entry := range entries {
				name := entry.Name()
				if directory == "." && (name == "backup" || name == "tmp" || strings.HasPrefix(name, ".kaven-restore-")) {
					continue
				}
				if directory != "." {
					name = path.Join(directory, name)
				}
				info, err := root.Lstat(name)
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
					continue
				}
				if len(directories) >= limit {
					return fmt.Errorf("more than %d directories", limit)
				}
				totalBytes += len(name)
				if totalBytes > maxDataDirectoryBytes {
					return fmt.Errorf("directory names exceed %d bytes", maxDataDirectoryBytes)
				}
				directories = append(directories, filepath.Join(dataDir, filepath.FromSlash(name)))
				if err := walk(name, depth+1); err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				break
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return nil, err
	}
	sort.Strings(directories)
	return directories, nil
}
