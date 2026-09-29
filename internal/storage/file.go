package storage

import (
	"io"
	"os"
	"path/filepath"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/pkg/utils"
)

type FileBackend struct{}

func (s *FileBackend) WriteFile(file []byte, image *models.Image) error {
	if err := config.ValidateImageLocation(); err != nil {
		return err
	}

	fileDir := config.GetImageLocation()

	path := GetImagePath(fileDir, image.ID.String())
	if exists, _ := utils.FileExists(path); exists {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), os.FileMode(0755)); err != nil {
		return err
	}

	if err := os.WriteFile(path, file, os.FileMode(0644)); err != nil {
		_ = os.Remove(path)
		return err
	}

	return nil
}

func (s *FileBackend) DestroyFile(image *models.Image) error {
	return os.Remove(GetImagePath(config.GetImageLocation(), image.ID.String()))
}

func (s *FileBackend) ReadFile(image models.Image) (io.ReadCloser, int64, error) {
	// Validate before deriving the path, for the same reason WriteFile does.
	//
	// Without this, GetImageLocation() returns "" and filepath.Join("", "ab/cd")
	// collapses to the RELATIVE path "ab/cd" -- so the read is attempted against
	// the process working directory and fails with a bare "no such file or
	// directory" that says nothing about the real cause. Worse, the write path
	// had the same hole before it was validated, which is how files ended up
	// scattered in the working directory in the first place (#649).
	if err := config.ValidateImageLocation(); err != nil {
		return nil, 0, err
	}

	fileDir := config.GetImageLocation()
	path := GetImagePath(fileDir, image.ID.String())
	stat, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}

	file, err := os.Open(path)
	return file, stat.Size(), err
}

func GetImagePath(imageDir string, id string) string {
	return filepath.Join(imageDir, shardedKey(id))
}
