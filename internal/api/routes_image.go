package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service"
	"github.com/stashapp/stash-box/internal/storage"
	"github.com/stashapp/stash-box/internal/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/go-chi/chi/v5"
	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/image"
	"github.com/stashapp/stash-box/internal/image/cache"
	"github.com/stashapp/stash-box/pkg/logger"
)

const tracerName = "github.com/stashapp/stash-box/internal/api"

type imageRoutes struct {
	fac service.Factory
}

func (rs imageRoutes) Routes() chi.Router {
	r := chi.NewRouter()

	r.Get("/{uuid}", rs.image)
	r.Get("/site/{uuid}", rs.siteImage)

	return r
}

func (rs imageRoutes) image(w http.ResponseWriter, r *http.Request) {
	uuid, err := uuid.FromString(chi.URLParam(r, "uuid"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	requestedSize, err := getImageSize(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}

	cacheManager := cache.GetCacheManager()

	// Check for cached image
	if requestedSize != 0 && cacheManager != nil {
		reader, err := cacheManager.Read(uuid, requestedSize)

		if err == nil {
			defer reader.Close()

			w.Header().Add("Cache-Control", "max-age=604800000")
			// Use http.ServeContent for *os.File to enable sendfile syscall
			if file, ok := reader.(*os.File); ok {
				http.ServeContent(w, r, "", time.Time{}, file)
				return
			}
			if _, err := io.Copy(w, reader); err != nil {
				logger.Debugf("failed to read cached image: %v", err)
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
	}

	ctx := r.Context()
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("image.id", uuid.String()))

	imageService := rs.fac.Image()
	databaseImage, err := imageService.Find(ctx, uuid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if databaseImage == nil {
		http.NotFound(w, r)
		return
	}

	_, readSpan := otel.Tracer(tracerName).Start(ctx, "image.Read")
	reader, size, err := imageService.Read(*databaseImage)
	tracing.RecordError(readSpan, err)
	readSpan.End()
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer reader.Close()

	if databaseImage.Width == -1 {
		w.Header().Add("Content-Type", "image/svg+xml")
		w.Header().Add("Content-Security-Policy", "script-src 'none'")
	}
	w.Header().Add("Cache-Control", "max-age=604800000")

	// Resize image
	if shouldResize(databaseImage, requestedSize) {
		_, span := otel.Tracer(tracerName).Start(ctx, "image.Resize")
		span.SetAttributes(attribute.Int("image.requested_size", requestedSize))
		data, err := image.Resize(reader, requestedSize, databaseImage, size)
		tracing.RecordError(span, err)
		span.End()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Set the content type for the bytes we are about to write (#605).
		//
		// The resizer does not preserve the source format: a PNG comes back as
		// WebP (lossless, which does keep the alpha channel) and everything else
		// comes back as JPEG. Without an explicit header, Go sniffs the body and
		// the response is labelled application/octet-stream, which browsers
		// refuse to render -- so a studio logo requested at a reduced size did
		// not display at all, and a logo that did display lost its
		// transparency in clients that fell back to a renderer.
		//
		// The stored file is untouched: this only affects the response.
		w.Header().Set("Content-Type", ResizedContentType(data))

		_, writeSpan := otel.Tracer(tracerName).Start(ctx, "image.WriteResponse")
		_, werr := w.Write(data)
		tracing.RecordError(writeSpan, werr)
		writeSpan.End()
		if werr != nil {
			http.Error(w, werr.Error(), http.StatusInternalServerError)
		}
		if cacheManager != nil {
			_ = cacheManager.Write(databaseImage.ID, requestedSize, data)
		}
		return
	}

	// Serve full image - use http.ServeContent for *os.File to enable sendfile syscall
	_, writeSpan := otel.Tracer(tracerName).Start(ctx, "image.WriteResponse")
	defer writeSpan.End()
	if file, ok := reader.(*os.File); ok {
		http.ServeContent(w, r, "", time.Time{}, file)
		return
	}
	if _, err := io.Copy(w, reader); err != nil {
		tracing.RecordError(writeSpan, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (rs imageRoutes) siteImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "uuid")
	siteID, err := uuid.FromString(id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	ctx := r.Context()
	site, err := rs.fac.Site().GetByID(ctx, siteID)
	if err != nil {
		return
	}

	data, err := storage.GetSiteIcon(r.Context(), *site)
	if err != nil {
		logger.Error("Couldn't get favicon:", err)
	}

	if data == nil {
		w.Header().Add("Cache-Control", "max-age=86400")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	contentType := faviconContentType(data)
	w.Header().Set("Content-Type", contentType)
	if contentType == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "script-src 'none'")
	}
	w.Header().Add("Cache-Control", "max-age=604800000")
	//nolint
	w.Write(data)
}

// ResizedContentType returns the Content-Type for bytes produced by
// image.Resize.
//
// The format is decided by the resizer, not by the stored file: PNG in, lossless
// WebP out (which preserves the alpha channel a studio logo needs), everything
// else in, JPEG out. Detecting from the bytes rather than from the source image
// is the only reliable option, because the two do not correspond.
//
// Falls back to image/jpeg, matching the resizer's default output, so an
// unrecognised body is still labelled as an image rather than as
// application/octet-stream -- which browsers refuse to render (#605).
func ResizedContentType(data []byte) string {
	if len(data) >= 12 &&
		string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 8 && string(data[1:4]) == "PNG" {
		return "image/png"
	}
	return "image/jpeg"
}

func faviconContentType(data []byte) string {
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("<svg")) ||
		(bytes.HasPrefix(trimmed, []byte("<?xml")) && bytes.Contains(trimmed, []byte("<svg"))) {
		return "image/svg+xml"
	}
	return http.DetectContentType(data)
}

// Limit allowed sizes to prevent abuse
var allowedSizes = []int{300, 600, 1280}

func getImageSize(r *http.Request) (int, error) {
	maxSize := 0
	querySize := r.FormValue("size")
	switch {
	case querySize == "full":
	// Skip resize
	case querySize != "":
		size, err := strconv.Atoi(querySize)
		if err != nil || !slices.Contains(allowedSizes, size) {
			return 0, err
		}
		return size, err
	case config.GetImageMaxSize() != nil:
		maxSize = *config.GetImageMaxSize()
	}

	return maxSize, nil
}

// shouldResize returns true if resize config is enabled, the size to resize to is not zero,
// the image is not below the minimum size to ignore, and the image is larger than the minimum
// size to resize down to.
func shouldResize(image *models.Image, requestedSize int) bool {
	config := config.GetImageResizeConfig()
	minSize := config.MinSize
	return config.Enabled && requestedSize != 0 &&
		(image.Width > minSize || image.Height > minSize) &&
		(image.Width > requestedSize || image.Height > requestedSize)
}
