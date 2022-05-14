/*
Copyright 2022 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	awssess "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	ggcrv1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/promo-tools/v3/mirror/pkg/types"
)

const (
	// BucketBasename is the base name to use when creating buckets in a region
	BucketBasename       = "registry-k8s-io-"
	uploadTimeoutSeconds = 600
	partSize             = 10 * 1024 * 1024 // 10MB chunks...
)

var (
	// ErrNoSuchBucket is returned when attempting to create a bucket writer
	// and the underlying bucket does not exist
	ErrNoSuchBucket = errors.New("No such bucket")
)

// BucketName returns the standardized bucket name from a bucket prefix and
// region.
func BucketName(prefix, region string) string {
	return fmt.Sprintf("%s%s%s", prefix, BucketBasename, region)
}

// bucketMirror maintains a session to write to a specific bucket in a specific
// AWS region
type bucketMirror struct {
	bucket   string
	region   string
	svc      *s3.S3
	uploader *s3manager.Uploader
}

// NewBucketMirror returns a layer writer that will write layers to a bucket in
// a particular AWS region.
func NewBucketMirror(
	region string,
	bucket string, // name of the bucket to sync to
) (types.ImageMirrorer, error) {
	sess, err := awssess.NewSession(
		&aws.Config{
			Region: aws.String(region),
		},
	)
	if err != nil {
		return nil, err
	}
	svc := s3.New(sess)
	uploader := s3manager.NewUploaderWithClient(
		svc,
		func(u *s3manager.Uploader) {
			u.PartSize = partSize
			u.LeavePartsOnError = false
		},
	)
	m := &bucketMirror{
		bucket:   bucket,
		region:   region,
		svc:      svc,
		uploader: uploader,
	}
	if !m.bucketExists() {
		return nil, ErrNoSuchBucket
	}
	return m, nil
}

// ID returns the identifier for the mirror
func (m *bucketMirror) ID() string {
	return "aws:" + m.region
}

// bucketExists returns true if the writer's bucket exists, false otherwise
func (m *bucketMirror) bucketExists() bool {
	ctx := context.TODO()
	input := s3.HeadBucketInput{
		Bucket: aws.String(m.bucket),
	}
	_, err := m.svc.HeadBucketWithContext(ctx, &input)
	if err != nil {
		aerr, ok := err.(awserr.Error)
		if ok && aerr.Code() != s3.ErrCodeNoSuchBucket {
			logrus.Warnf("failed to check bucket existence: %v", err)
		}
		return false
	}
	return true
}

// objectExists returns true if the specified object exists, false otherwise
func (m *bucketMirror) objectExists(
	key string,
) bool {
	ctx := context.TODO()
	input := s3.HeadObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(key),
	}
	_, err := m.svc.HeadObjectWithContext(ctx, &input)
	// Technically, the HEAD call can return a 404 or a 403, but we will assume
	// a 404 here...
	return err == nil
}

// Mirror examines an Image and uploads its Layers to backend mirror storage
func (m *bucketMirror) Mirror(
	imageURI string, // the image URI/reference
	image ggcrv1.Image,
) error {
	layers, err := image.Layers()
	if err != nil {
		return err
	}
	for _, layer := range layers {
		digest, err := layer.Digest()
		if err != nil {
			return err
		}
		digestBytes, err := digest.MarshalText()
		if err != nil {
			return err
		}
		objKey := string(digestBytes)
		if m.objectExists(objKey) {
			logrus.Debugf(
				"[%s] layer %s already exists in bucket %s",
				m.region, objKey, m.bucket,
			)
			continue
		}
		if err := m.writeLayer(objKey, layer); err != nil {
			return err
		}
		logrus.Infof(
			"[%s] wrote layer %s to bucket %s",
			m.region, objKey, m.bucket,
		)
	}
	return nil
}

// writeLayer writes the supplied Layer to backend storage
func (m *bucketMirror) writeLayer(
	objKey string,
	layer ggcrv1.Layer,
) error {
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, uploadTimeoutSeconds)
	defer cancel()
	contentStream, err := layer.Compressed()
	if err != nil {
		return err
	}
	defer contentStream.Close()

	input := s3manager.UploadInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(objKey),
		Body:   contentStream,
	}
	_, err = m.uploader.UploadWithContext(ctx, &input)
	return err
}
