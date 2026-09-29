package image

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/image/cache"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/loadutil"
	"github.com/stashapp/stash-box/internal/storage"
)

type Image struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
}

func NewImage(queries *queries.Queries, withTxn queries.WithTxnFunc) *Image {
	return &Image{
		queries: queries,
		withTxn: withTxn,
	}
}

// WithTxn executes a function within a transaction
func (s *Image) WithTxn(fn func(*queries.Queries) error) error {
	return s.withTxn(fn)
}

func (s *Image) Create(ctx context.Context, input models.ImageCreateInput) (*models.Image, error) {
	UUID, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}

	// Generate uuid that does not start with AD to prevent adblock issues
	for strings.HasPrefix(UUID.String(), "ad") {
		UUID, err = uuid.NewV7()
		if err != nil {
			return nil, err
		}
	}

	newImage := models.Image{
		ID: UUID,
	}

	// set RemoteURL from URL
	if input.URL != nil {
		newImage.RemoteURL = input.URL
	}

	// handle image upload
	var file []byte
	if input.File != nil {
		if input.File.Size > int64(10*1024*1024) {
			return nil, errors.New("file too big")
		}

		file = make([]byte, input.File.Size)
		if _, err := input.File.File.Read(file); err != nil {
			return nil, err
		}
		fileReader := bytes.NewReader(file)

		checksum, err := calculateChecksum(fileReader)
		if err != nil {
			return nil, err
		}

		// check if image already exists with this checksum
		existing, err := s.FindByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}

		// if image already exists, just return it
		//
		// ...but only if its file is actually there (#948).
		//
		// The row and the file are two separate facts, and the row is written
		// first -- the write below is deliberately after the insert, so that a
		// failed write cannot leave an orphan file that DestroyUnusedImages
		// cannot find (#738). The cost of that ordering is that a row can exist
		// with no bytes behind it.
		//
		// That state is permanent. Every later upload of the same bytes
		// short-circuits here and returns the broken row, so re-uploading can
		// never repair it -- which is the reported symptom: rare, always the
		// same image, immune to retrying, and needing a site admin to add the
		// image by hand.
		//
		// A missing file is therefore treated as absent, and the dead row is
		// deleted so the retry below runs cleanly. The id cannot be reused:
		// the row is keyed by id and holds the checksum, so overwriting it
		// would mean writing new bytes under a path the old id already claims.
		// Deleting and reinserting gives the repaired image a fresh id, and
		// DeleteImage also drops the join-table rows, so the broken row stops
		// appearing anywhere.
		if existing != nil {
			exists, err := storage.Image().FileExists(existing)
			if err != nil {
				return nil, err
			}
			if exists {
				return existing, nil
			}

			if err := s.queries.DeleteImage(ctx, existing.ID); err != nil {
				return nil, err
			}
		}

		// set the checksum in the new image
		newImage.Checksum = checksum

		if _, err = fileReader.Seek(0, 0); err != nil {
			return nil, err
		}

		if err := populateImageDimensions(fileReader, &newImage); err != nil {
			return nil, err
		}
	} else if input.URL == nil {
		return nil, errors.New("missing URL or file")
	}

	params := queries.CreateImageParams{
		ID:       newImage.ID,
		Checksum: newImage.Checksum,
		Width:    newImage.Width,
		Height:   newImage.Height,
		Url:      newImage.RemoteURL,
	}

	dbImage, err := s.queries.CreateImage(ctx, params)
	if err != nil {
		return nil, err
	}

	image := converter.ImageToModelPtr(dbImage)

	// Write the file only once the row exists, and only when this call is the
	// one that created it.
	//
	// CreateImage upserts on the unique checksum index (#738), so a concurrent
	// upload of the same bytes can return the OTHER call's row -- a different
	// id. Writing the file before the insert would leave bytes on disk under an
	// id that no row references, which DestroyUnusedImages cannot reclaim
	// because it walks the images table.
	//
	// The reverse order has its own hazard: if WriteFile fails the row is
	// already committed, pointing at a file that does not exist. That is the
	// lesser of the two -- a missing file is retried by re-uploading, whereas
	// an orphan file is invisible to the reaper and leaks forever. The insert
	// therefore goes first and the write is best-effort with the failure
	// surfaced, so the caller can retry.
	if input.File != nil && image.ID == newImage.ID {
		if err := storage.Image().WriteFile(file, &newImage); err != nil {
			return nil, err
		}
	}

	return image, nil
}

func (s *Image) Destroy(ctx context.Context, id uuid.UUID) error {
	image, err := s.Find(ctx, id)
	if err != nil {
		return err
	}

	if err := s.queries.DeleteImage(ctx, id); err != nil {
		return err
	}

	// delete the file. Suppress any error
	_ = storage.Image().DestroyFile(image)

	// Clear image from cache
	cacheManager := cache.GetCacheManager()
	if cacheManager != nil {
		_ = cacheManager.Delete(id)
	}

	return nil
}

func (s *Image) Find(ctx context.Context, id uuid.UUID) (*models.Image, error) {
	dbImage, err := s.queries.FindImage(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return converter.ImageToModelPtr(dbImage), nil
}

func (s *Image) FindBySceneID(ctx context.Context, sceneID uuid.UUID) ([]models.Image, error) {
	dbImages, err := s.queries.FindImagesBySceneID(ctx, sceneID)
	if err != nil {
		return nil, err
	}
	return converter.ImagesToModels(dbImages), nil
}

func (s *Image) FindByChecksum(ctx context.Context, checksum string) (*models.Image, error) {
	dbImage, err := s.queries.FindImageByChecksum(ctx, checksum)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return converter.ImageToModelPtr(dbImage), nil
}

func (s *Image) DestroyUnusedImages(ctx context.Context) error {

	unused, err := s.FindUnused(ctx)
	if err != nil {
		return err
	}

	cacheManager := cache.GetCacheManager()

	for len(unused) > 0 {
		for _, i := range unused {
			err = s.Destroy(ctx, i.ID)
			if err != nil {
				return err
			}
			// Clear image from cache
			if cacheManager != nil {
				_ = cacheManager.Delete(i.ID)
			}
		}

		unused, err = s.FindUnused(ctx)
		if err != nil {
			return err
		}
	}

	return nil

}

func (s *Image) DestroyUnusedImage(ctx context.Context, imageID uuid.UUID) error {
	unused, err := s.IsUnused(ctx, imageID)
	if err != nil {
		return err
	}

	if unused {
		err = s.Destroy(ctx, imageID)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Image) FindUnused(ctx context.Context) ([]models.Image, error) {
	dbImages, err := s.queries.FindUnusedImages(ctx)
	if err != nil {
		return nil, err
	}
	return converter.ImagesToModels(dbImages), nil
}

func (s *Image) IsUnused(ctx context.Context, imageID uuid.UUID) (bool, error) {
	return s.queries.IsImageUnused(ctx, imageID)
}

// Dataloader for images by ids
func (s *Image) LoadIds(ctx context.Context, ids []uuid.UUID) ([]*models.Image, []error) {
	return loadutil.One(ids,
		func(ids []uuid.UUID) ([]queries.Image, error) { return s.queries.FindImagesByIds(ctx, ids) },
		func(image queries.Image) uuid.UUID { return image.ID },
		converter.ImageToModelPtr,
	)
}

// Dataloder for images for scenes
func (s *Image) LoadBySceneIds(ctx context.Context, ids []uuid.UUID) ([][]uuid.UUID, []error) {
	return loadutil.Many(ids,
		func(ids []uuid.UUID) ([]queries.SceneImage, error) { return s.queries.FindImageIdsBySceneIds(ctx, ids) },
		func(image queries.SceneImage) uuid.UUID { return image.SceneID },
		func(image queries.SceneImage) uuid.UUID { return image.ImageID },
	)
}

// Dataloder for images for performers
func (s *Image) LoadByPerformerIds(ctx context.Context, ids []uuid.UUID) ([][]uuid.UUID, []error) {
	return loadutil.Many(ids,
		func(ids []uuid.UUID) ([]queries.PerformerImage, error) {
			return s.queries.FindImageIdsByPerformerIds(ctx, ids)
		},
		func(image queries.PerformerImage) uuid.UUID { return image.PerformerID },
		func(image queries.PerformerImage) uuid.UUID { return image.ImageID },
	)
}

func (s *Image) FindByPerformerID(ctx context.Context, performerID uuid.UUID) ([]models.Image, error) {
	dbImages, err := s.queries.GetPerformerImages(ctx, performerID)
	if err != nil {
		return nil, err
	}
	return converter.ImagesToModels(dbImages), nil
}

func (s *Image) FindByStudioID(ctx context.Context, studioID uuid.UUID) ([]models.Image, error) {
	dbImages, err := s.queries.FindImagesByStudioID(ctx, studioID)
	if err != nil {
		return nil, err
	}
	return converter.ImagesToModels(dbImages), nil
}

func (s *Image) LoadByStudioIds(ctx context.Context, ids []uuid.UUID) ([][]uuid.UUID, []error) {
	return loadutil.Many(ids,
		func(ids []uuid.UUID) ([]queries.StudioImage, error) {
			return s.queries.FindImageIdsByStudioIds(ctx, ids)
		},
		func(image queries.StudioImage) uuid.UUID { return image.StudioID },
		func(image queries.StudioImage) uuid.UUID { return image.ImageID },
	)
}

func (s *Image) Read(image models.Image) (io.ReadCloser, int64, error) {
	return storage.Image().ReadFile(image)
}
