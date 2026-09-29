package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/models"
)

type S3Backend struct{}

func (s *S3Backend) WriteFile(file []byte, image *models.Image) error {
	s3config := config.GetS3Config()
	headers := s3config.UploadHeaders

	minioClient, err := minio.New(s3config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s3config.AccessKey, s3config.Secret, ""),
		Secure: true,
	})
	if err != nil {
		return fmt.Errorf("creating minio client: %w", err)
	}

	if err := uploadS3File(minioClient, file, s3config.Bucket, image.ID.String(), headers); err != nil {
		return fmt.Errorf("uploading to s3: %w", err)
	}

	return nil
}

func (s *S3Backend) DestroyFile(image *models.Image) error {
	s3config := config.GetS3Config()
	minioClient, err := minio.New(s3config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s3config.AccessKey, s3config.Secret, ""),
		Secure: true,
	})
	if err != nil {
		return err
	}

	id := image.ID.String()
	path := shardedKey(id)
	err = minioClient.RemoveObject(context.TODO(), s3config.Bucket, path, minio.RemoveObjectOptions{})

	if err != nil {
		return err
	}

	return nil
}

// FileExists reports whether the object is present in the bucket.
//
// StatObject is the S3 equivalent of the os.Stat in the file backend, and
// NoSuchKey is S3's "not found" -- unlike the file backend, a misconfigured
// endpoint surfaces as an error here rather than as "absent", which is the
// behaviour the image service relies on to tell the two apart (#948).
func (s *S3Backend) FileExists(image *models.Image) (bool, error) {
	if image.RemoteURL != nil {
		return true, nil
	}

	s3config := config.GetS3Config()
	minioClient, err := minio.New(s3config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s3config.AccessKey, s3config.Secret, ""),
		Secure: true,
	})
	if err != nil {
		return false, err
	}

	_, err = minioClient.StatObject(
		context.TODO(),
		s3config.Bucket,
		shardedKey(image.ID.String()),
		minio.StatObjectOptions{},
	)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func uploadS3File(client *minio.Client, file []byte, bucket string, id string, headers map[string]string) error {
	ctx := context.TODO()

	// SVG is not correctly detected so we set it manually if the file is xml
	contentType := http.DetectContentType(file)
	if contentType == "text/xml; charset=utf-8" || contentType == "text/plain; charset=utf-8" {
		contentType = "image/svg+xml"
	}

	path := shardedKey(id)
	_, err := client.PutObject(
		ctx,
		bucket,
		path,
		bytes.NewReader(file),
		int64(len(file)),
		minio.PutObjectOptions{
			ContentType:  contentType,
			UserMetadata: headers,
		},
	)

	return err
}

func (s *S3Backend) ReadFile(image models.Image) (io.ReadCloser, int64, error) {
	ctx := context.TODO()

	s3config := config.GetS3Config()
	minioClient, err := minio.New(s3config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(s3config.AccessKey, s3config.Secret, ""),
		Secure: true,
	})
	if err != nil {
		return nil, 0, err
	}

	id := image.ID.String()
	path := shardedKey(id)

	object, err := minioClient.GetObject(
		ctx,
		s3config.Bucket,
		path,
		minio.GetObjectOptions{},
	)
	if err != nil {
		return nil, 0, err
	}

	stat, err := object.Stat()
	if err != nil {
		return nil, 0, err
	}

	return object, stat.Size, err
}
