package mediaplane

// The producer's ENCODE half (plan phase 10, slice 10c-2). The fetch half -- HTTP, robots, bounds, the source
// URL on the row -- is the next slice and is NOT here; what is here is the one call that turns source bytes
// into stored bytes, so that the format question is answered by the instance's own pipeline rather than by a
// second encoder written for this feature.
//
// WHY IT DELEGATES. `internal/image.Resize` is this instance's answer to "make a smaller picture": libvips,
// `image_jpeg_quality` from config (default 75), webp for a PNG source and jpeg otherwise. Writing a second
// encoder here would mean two answers to that question, and the two would disagree in exactly the cases nobody
// checks -- which is the shape of the defect this slice exists to correct: the first version of the layout said
// "webp at 80", from the measurement's Pillow, and this instance CANNOT ENCODE WEBP AT ALL. The measurement had
// validated a codec the binary does not have.
//
// WHAT THE OUTPUT IS, SAID PLAINLY: whatever the instance's resizer produces. That is usually JPEG (most
// upstream photographs are JPEG), and webp only when the source was a PNG, where the resizer exports lossless
// webp. The store sniffs it. The consequence for the storage table in the plan is that the variant sizes are
// the resizer's, not Pillow's webp sizes: the same order of magnitude, and the verdict is unchanged, because
// what the table decides is whether a bounded thumbnail fits in 1.6 TB (it does, by two orders of magnitude).

import (
	"errors"
	"fmt"
	"io"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/image"
	"github.com/stashapp/stash-box/internal/models"
)

// ErrVariantWidthNotStored is returned when a caller asks the producer for a width the ladder does not keep.
//
// It is a refusal rather than a rounding: silently producing 600 px for a request of 512 would put a file at a
// path that names a width nobody asked for, and the next reader of the directory would believe it.
var ErrVariantWidthNotStored = errors.New("this width is not one the ladder stores")

// ProduceVariant turns source image bytes of a known length into the stored bytes for one width.
//
// `size` is the byte length of the source, because the instance's resizer takes a length (it reads exactly that
// many bytes from the reader): a caller that does not know its own length must read the source into a buffer
// first, which is also where a byte bound belongs, and that bound is the fetch half's job.
//
// The result is written nowhere: this function answers "what bytes", and `Store.WriteVariant` answers "where".
// Splitting them keeps the encode testable without a filesystem and the storage testable without libvips.
func ProduceVariant(src io.Reader, size int64, width int) ([]byte, error) {
	if !IsVariantWidth(width) {
		return nil, fmt.Errorf("%w: %d (the ladder stores %v)", ErrVariantWidthNotStored, width, VariantWidths)
	}
	if size <= 0 {
		return nil, fmt.Errorf("refusing to decode %d bytes: a source with no length is a source that was not "+
			"fetched", size)
	}
	// A non-nil empty models.Image: the unix resizer ignores it and the windows one takes it, so nil would be
	// a latent panic on one platform for the sake of saving an allocation.
	return image.Resize(src, width, &models.Image{}, size)
}

// ProduceAndStore is the producer's encode-and-write step, in the order that makes the store's invariants
// apply: the bytes are sniffed and refused BEFORE anything is written, so an upstream answer in an unexpected
// shape leaves no file behind.
func (s *Store) ProduceAndStore(id uuid.UUID, width int, src io.Reader, size int64) (string, error) {
	data, err := ProduceVariant(src, size, width)
	if err != nil {
		return "", err
	}
	return s.WriteVariant(id, width, data)
}
