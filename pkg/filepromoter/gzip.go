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
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"k8s.io/klog"
)

// minCompressSize is the minimum size for even trying to compress files
const minCompressSize = 8 * 1024

// minCompressionRatio is the ratio we must obtain to consider a file compressible
const minCompressionRatio = 0.9

// maybeGzip will try to gzip the file, and will return a temp file if compression was worthwhile
// If it returns a tempfile, that tempfile should be deleted by the caller
func maybeGzip(localFile string) (string, error) {
	originalStat, err := os.Stat(localFile)
	if err != nil {
		return "", fmt.Errorf("error getting stat of %q: %v", localFile, err)
	}

	if originalStat.Size() < minCompressSize {
		klog.V(2).Infof("file %s is too small to compress", localFile)
		return "", nil
	}

	tmpfile, err := gzipToTempFile(localFile)
	if err != nil {
		return "", fmt.Errorf("error compressing file: %v", err)
	}

	removeTempfile := true
	defer func() {
		if removeTempfile {
			loggedRemove(tmpfile)
		}
	}()

	newStat, err := os.Stat(tmpfile)
	if err != nil {
		return "", fmt.Errorf("error getting stat of %q: %v", tmpfile, err)
	}

	if float32(newStat.Size())/float32(originalStat.Size()) > minCompressionRatio {
		klog.V(2).Infof("%s did not compress sufficiently (%d to %d)", localFile, originalStat.Size(), newStat.Size())
		return "", nil
	}

	klog.V(2).Infof("%s compressed sufficiently (%d to %d), uploading compressed", localFile, originalStat.Size(), newStat.Size())

	removeTempfile = false
	return tmpfile, nil
}

// loggedClose will close the file, printing a warning if the close failed
func loggedClose(f *os.File) {
	if err := f.Close(); err != nil {
		klog.Warningf("error closing %s: %v", f.Name(), err)
	}
}

// loggedRemove will delete the specified tempfile, printing a warning on failure
func loggedRemove(p string) {
	if err := os.Remove(p); err != nil {
		klog.Warningf("error deleting temp file %s: %v", p, err)
	}
}

// gzipToTempFile will gzip the specified file to a tempfile
func gzipToTempFile(p string) (string, error) {
	in, err := os.Open(p)
	if err != nil {
		return "", fmt.Errorf("error opening file %s: %v", p, err)
	}

	defer loggedClose(in)

	tmpfile, err := ioutil.TempFile("", "")
	if err != nil {
		return "", fmt.Errorf("error creating temp file for compression: %v", err)
	}
	removeTempfile := true
	defer func() {
		if removeTempfile {
			loggedRemove(tmpfile.Name())
		}
	}()

	defer loggedClose(tmpfile)

	// TODO: Allow more compression?  Default to more compression?
	gw := gzip.NewWriter(tmpfile)

	if _, err := io.Copy(gw, in); err != nil {
		gw.Close()
		return "", fmt.Errorf("error compressing file: %v", err)
	}

	if err := gw.Close(); err != nil {
		return "", fmt.Errorf("error compressing file (closing): %v", err)
	}

	removeTempfile = false
	return tmpfile.Name(), nil
}
