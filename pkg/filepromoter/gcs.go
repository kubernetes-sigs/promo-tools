/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package filepromoter

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strconv"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"k8s.io/klog"
	api "sigs.k8s.io/k8s-container-image-promoter/pkg/api/files"
)

const (
	MetadataKeyUncompressedSize   = "uncompressed-size"
	MetadataKeyUncompressedSHA256 = "uncompressed-sha256"
	MetadataKeySHA256             = "sha256"
)

type gcsSyncFilestore struct {
	filestore *api.Filestore
	client    *storage.Client
	bucket    string
	prefix    string
}

// OpenReader opens an io.ReadCloser for the specified file
func (s *gcsSyncFilestore) OpenReader(
	ctx context.Context,
	name string) (io.ReadCloser, error) {
	absolutePath := s.prefix + name
	return s.client.Bucket(s.bucket).Object(absolutePath).NewReader(ctx)
}

// UploadFileRaw uploads a local file to the specified destination.
// It will attempt to use transparent compression for faster/cheaper downloads.
func (s *gcsSyncFilestore) UploadFile(
	ctx context.Context,
	dest string,
	localFile string) error {

	sha256, err := computeSHA256ForFile(localFile)
	if err != nil {
		return err
	}

	stat, err := os.Stat(localFile)
	if err != nil {
		return fmt.Errorf("error getting stat for %q: %v", localFile, err)
	}

	metadata := map[string]string{}

	tmpfile, err := maybeGzip(localFile)
	if err != nil {
		return fmt.Errorf("error compressing file: %v", err)
	}
	if tmpfile == "" {
		// Not worth compressing

		metadata[MetadataKeySHA256] = sha256

		return s.uploadFileRaw(ctx, dest, localFile, "", "", metadata)
	}

	defer loggedRemove(tmpfile)

	contentEncoding := "gzip"
	contentType := "application/octet-stream"

	metadata[MetadataKeyUncompressedSHA256] = sha256
	metadata[MetadataKeyUncompressedSize] = strconv.FormatInt(stat.Size(), 10)

	return s.uploadFileRaw(ctx, dest, tmpfile, contentEncoding, contentType, metadata)
}

// uploadFileRaw uploads a local file to the specified destination
// it does not perform any smart compression etc.
func (s *gcsSyncFilestore) uploadFileRaw(
	ctx context.Context,
	dest string,
	localFile string,
	contentEncoding string,
	contentType string,
	metadata map[string]string) error {
	absolutePath := s.prefix + dest

	gcsURL := "gs://" + s.bucket + "/" + absolutePath

	in, err := os.Open(localFile)
	if err != nil {
		return fmt.Errorf("error opening %q: %v", localFile, err)
	}
	defer func() {
		if err := in.Close(); err != nil {
			klog.Warningf("error closing %q: %v", localFile, err)
		}
	}()

	// Compute crc32 checksum for upload integrity
	var fileCRC32C uint32
	{
		hasher := crc32.New(crc32.MakeTable(crc32.Castagnoli))
		if _, err := io.Copy(hasher, in); err != nil {
			return fmt.Errorf("error computing crc32 checksum: %v", err)
		}
		fileCRC32C = hasher.Sum32()

		if _, err := in.Seek(0, 0); err != nil {
			return fmt.Errorf("error rewinding in file: %v", err)
		}
	}

	klog.Infof("uploading to %s", gcsURL)

	w := s.client.Bucket(s.bucket).Object(absolutePath).NewWriter(ctx)

	w.CRC32C = fileCRC32C
	w.SendCRC32C = true

	w.ContentEncoding = contentEncoding
	w.ContentType = contentType

	w.Metadata = metadata

	// Much bigger chunk size for faster uploading
	w.ChunkSize = 128 * 1024 * 1024

	if _, err := io.Copy(w, in); err != nil {
		if err2 := w.Close(); err2 != nil {
			klog.Warningf("error closing upload stream: %v", err)
			// TODO: Try to delete the possibly partially written file?
		}
		return fmt.Errorf("error uploading to %q: %v", gcsURL, err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("error uploading to %q: %v", gcsURL, err)
	}

	return nil
}

// ListFiles returns all the file artifacts in the filestore, recursively.
func (s *gcsSyncFilestore) ListFiles(
	ctx context.Context) (map[string]*syncFileInfo, error) {
	files := make(map[string]*syncFileInfo)

	q := &storage.Query{Prefix: s.prefix}
	klog.Infof("listing files in bucket %s with prefix %q", s.bucket, s.prefix)
	it := s.client.Bucket(s.bucket).Objects(ctx, q)
	for {
		obj, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf(
				"error listing objects in %q: %v",
				s.filestore.Base, err)
		}
		name := obj.Name
		if !strings.HasPrefix(name, s.prefix) {
			return nil, fmt.Errorf(
				"found object %q without prefix %q",
				name, s.prefix)
		}

		file := &syncFileInfo{}
		file.AbsolutePath = "gs://" + s.bucket + "/" + obj.Name
		file.RelativePath = strings.TrimPrefix(name, s.prefix)

		sha256 := obj.Metadata[MetadataKeySHA256]
		if sha256 == "" {
			sha256 = obj.Metadata[MetadataKeyUncompressedSHA256]
		}
		file.SHA256 = sha256

		file.Size = obj.Size
		uncompressedSize := obj.Metadata[MetadataKeyUncompressedSize]
		if uncompressedSize != "" {
			n, err := strconv.ParseInt(uncompressedSize, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("unable to parse attribute uncompressed-size=%q", uncompressedSize)
			}
			file.Size = n
		}

		file.filestore = s

		files[file.RelativePath] = file
	}

	return files, nil
}
