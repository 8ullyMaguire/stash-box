package storage

import (
	"io"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/models"
)

func shardedKey(id string) string {
	return id[0:2] + "/" + id[2:4] + "/" + id
}

type Backend interface {
	WriteFile(file []byte, image *models.Image) error
	DestroyFile(image *models.Image) error
	ReadFile(image models.Image) (io.ReadCloser, int64, error)

	// FileExists reports whether the bytes for this image are actually
	// retrievable from the backend.
	//
	// This exists because a database row and the file it names are two
	// separate facts, and only the first is transactional. Create commits the
	// row and then writes the file, so a row can exist with no file behind it.
	// Anything that treats "the row exists" as "the image is available" is
	// then wrong -- see the checksum short-circuit in Image.Create (#948).
	//
	// A remote_url image has no local file and is always considered present.
	FileExists(image *models.Image) (bool, error)
}

func Image() Backend {
	imageBackend := config.GetImageBackend()

	var backend Backend
	switch imageBackend {
	case config.FileBackend:
		backend = &FileBackend{}
	case config.S3Backend:
		backend = &S3Backend{}
	}

	return backend
}
