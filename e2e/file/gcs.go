package file

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"k8s.io/klog"
)

type FileList struct {
	Files []*FileInfo `json:"files"`
}

type FileInfo struct {
	AbsolutePath       string           `json:"absolutePath"`
	MD5                string           `json:"md5"`
	Size               int64            `json:"size"`
	ContentEncoding    string           `json:"contentEncoding,omitempty"`
	ContentDisposition string           `json:"contentDisposition,omitempty"`
	Attributes         []*FileAttribute `json:"attributes,omitempty"`
}

type FileAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ListFiles list all files beneath the storage baseURL
func ListFiles(baseURL string) (*FileList, error) {
	ctx := context.TODO()

	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf(
			"error parsing base %q: %v",
			baseURL, err)
	}

	if u.Scheme != "gs" {
		return nil, fmt.Errorf(
			"unrecognized scheme %q (supported schemes: gs://)",
			baseURL)
	}

	client, err := NewStorageClient()
	if err != nil {
		return nil, err
	}

	info := &FileList{}

	info.Files = []*FileInfo{}

	q := &storage.Query{Prefix: u.Path}
	klog.Infof("listing files in bucket %s with prefix %q", u.Host, u.Path)
	it := client.Bucket(u.Host).Objects(ctx, q)
	for {
		obj, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf(
				"error listing objects in %q: %v",
				baseURL, err)
		}
		name := obj.Name
		if !strings.HasPrefix(name, u.Path) {
			return nil, fmt.Errorf(
				"found object %q without prefix %q",
				name, u.Path)
		}

		file := &FileInfo{}
		file.AbsolutePath = "gs://" + u.Host + "/" + obj.Name
		if obj.MD5 == nil {
			return nil, fmt.Errorf("MD5 not set on file %q", file.AbsolutePath)
		}

		file.MD5 = hex.EncodeToString(obj.MD5)
		file.Size = obj.Size

		file.ContentEncoding = obj.ContentEncoding
		file.ContentDisposition = obj.ContentDisposition

		for k, v := range obj.Metadata {
			file.Attributes = append(file.Attributes, &FileAttribute{
				Name:  k,
				Value: v,
			})
		}
		sort.Slice(file.Attributes, func(i, j int) bool {
			return file.Attributes[i].Name < file.Attributes[j].Name
		})

		info.Files = append(info.Files, file)
	}

	sort.Slice(info.Files, func(i, j int) bool {
		return info.Files[i].AbsolutePath < info.Files[j].AbsolutePath
	})

	return info, nil
}

func NewStorageClient() (*storage.Client, error) {
	var opts []option.ClientOption

	ctx := context.TODO()
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("error building GCS client: %v", err)
	}

	return client, nil
}

// DeleteAllFiles deletes all files in the FileList
func DeleteAllFiles(fileList *FileList) error {
	ctx := context.TODO()

	for _, file := range fileList.Files {
		obj, err := toObject(file.AbsolutePath)
		if err != nil {
			return err
		}
		if err := obj.Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete %s: %v", file.AbsolutePath, err)
		}
	}

	return nil
}

// UploadFile writes contents to the object storage path p
func UploadFile(p string, contents []byte) error {
	ctx := context.TODO()

	obj, err := toObject(p)
	if err != nil {
		return err
	}

	w := obj.NewWriter(ctx)

	_, writeErr := w.Write(contents)

	closeErr := w.Close()

	if writeErr != nil {
		return fmt.Errorf("error writing %s: %v", p, writeErr)
	}

	if closeErr != nil {
		return fmt.Errorf("error closing %s: %v", p, closeErr)
	}

	return nil
}

func toObject(p string) (*storage.ObjectHandle, error) {
	u, err := url.Parse(p)
	if err != nil {
		return nil, fmt.Errorf(
			"error parsing %q: %v",
			p, err)
	}

	if u.Scheme != "gs" {
		return nil, fmt.Errorf(
			"unrecognized scheme %q (supported schemes: gs://)",
			p)
	}

	client, err := NewStorageClient()
	if err != nil {
		return nil, err
	}

	objectPath := strings.TrimPrefix(u.Path, "/")

	return client.Bucket(u.Host).Object(objectPath), nil
}
