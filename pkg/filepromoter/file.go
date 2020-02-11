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
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/ioutil"
	"os"

	"k8s.io/klog"
	api "sigs.k8s.io/k8s-container-image-promoter/pkg/api/files"
)

// syncFileInfo tracks a file during the synchronization operation
type syncFileInfo struct {
	RelativePath string
	AbsolutePath string

	// Some backends (GCS and S3) expose the MD5 of the content in metadata
	// This can allow skipping unnecessary copies.
	// Note: with multipart uploads or compression, the value is unobvious.
	MD5 string

	Size int64

	filestore syncFilestore
}

// SourceFile represents a file on GCS or the filesystem
type SourceFile interface {
	// Open returns a stream to read the contents of the file
	Open(ctx context.Context) (io.ReadCloser, error)

	// Path returns the absolute path to the file for log messages
	Path() string
}

// Open implements the SourceFile interface
func (f *syncFileInfo) Open(ctx context.Context) (io.ReadCloser, error) {
	return f.filestore.OpenReader(ctx, f.RelativePath)
}

// Path implements the SourceFile interface
func (f *syncFileInfo) Path() string {
	return f.AbsolutePath
}

// copyFileOp manages copying a single file
type copyFileOp struct {
	Source SourceFile
	Dest   *syncFileInfo

	ManifestFile *api.File
}

// Run implements SyncFileOp.Run
// nolint[gocyclo]
func (o *copyFileOp) Run(ctx context.Context) error {
	// Download to our temp file
	f, err := ioutil.TempFile("", "promoter")
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}
	tempFilename := f.Name()

	defer func() {
		if f != nil {
			if err := f.Close(); err != nil {
				klog.Warningf(
					"error closing temp file %q: %v",
					tempFilename, err)
			}
		}

		if err := os.Remove(tempFilename); err != nil {
			klog.Warningf(
				"unable to remove temp file %q: %v",
				tempFilename, err)
		}
	}()

	in, err := o.Source.Open(ctx)
	if err != nil {
		return fmt.Errorf("error reading %q: %v", o.Source.Path(), err)
	}
	defer in.Close()

	if _, err := io.Copy(f, in); err != nil {
		return fmt.Errorf(
			"error downloading %s: %v",
			o.Source.Path(), err)
	}
	// We close the file to be sure it is fully written
	if err := f.Close(); err != nil {
		return fmt.Errorf("error writing temp file %q: %v", tempFilename, err)
	}
	f = nil

	// Verify the source hash
	sha256, err := ComputeSHA256ForFile(tempFilename)
	if err != nil {
		return err
	}
	if sha256 != o.ManifestFile.SHA256 {
		return fmt.Errorf(
			"sha256 did not match for file %q: actual=%q expected=%q",
			o.Source.Path(), sha256, o.ManifestFile.SHA256)
	}

	// Upload to the destination
	if err := o.Dest.filestore.UploadFile(
		ctx, o.Dest.RelativePath, tempFilename); err != nil {
		return err
	}

	return nil
}

// String is the pretty-printer for an operation, as used by dry-run
func (o *copyFileOp) String() string {
	return fmt.Sprintf(
		"COPY %q to %q",
		o.Source.Path(), o.Dest.AbsolutePath)
}

// nolint[lll]
// ComputeSHA256ForFile returns the hex-encoded sha256 hash of the file named filename
func ComputeSHA256ForFile(filename string) (string, error) {
	hasher := sha256.New()
	return computeHashForFile(filename, hasher)
}

// ComputeMD5ForFile returns the hex-encoded md5 hash of the file named filename
func ComputeMD5ForFile(filename string) (string, error) {
	hasher := md5.New()
	return computeHashForFile(filename, hasher)
}

func computeHashForFile(filename string, hasher hash.Hash) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf(
			"error re-opening temp file %q: %v",
			filename, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			klog.Warningf(
				"error closing file %q: %v",
				filename, err)
		}
	}()

	if _, err := io.Copy(hasher, f); err != nil {
		return "", fmt.Errorf("error hashing file %q: %v", filename, err)
	}

	sha256 := hex.EncodeToString(hasher.Sum(nil))
	return sha256, nil
}
