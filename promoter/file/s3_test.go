/*
Copyright 2026 The Kubernetes Authors.

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

package file

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
)

type fakeS3Client struct {
	listObjectsV2Calls []string
}

func (f *fakeS3Client) GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	panic("unexpected GetObject call")
}

func (f *fakeS3Client) PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	panic("unexpected PutObject call")
}

func (f *fakeS3Client) ListObjectsV2(_ context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.listObjectsV2Calls = append(f.listObjectsV2Calls, aws.ToString(params.ContinuationToken))

	switch aws.ToString(params.ContinuationToken) {
	case "":
		return &s3.ListObjectsV2Output{
			Contents: []types.Object{
				{
					Key:  aws.String("prefix/one.txt"),
					ETag: aws.String(`"0123456789abcdef0123456789abcdef"`),
					Size: aws.Int64(11),
				},
			},
			IsTruncated:           aws.Bool(true),
			NextContinuationToken: aws.String("page-2"),
		}, nil
	case "page-2":
		return &s3.ListObjectsV2Output{
			Contents: []types.Object{
				{
					Key:  aws.String("prefix/two.txt"),
					ETag: aws.String(`"fedcba9876543210fedcba9876543210"`),
					Size: aws.Int64(22),
				},
			},
			IsTruncated: aws.Bool(false),
		}, nil
	default:
		panic("unexpected continuation token")
	}
}

func TestS3ListFilesPaginatesAllResults(t *testing.T) {
	t.Parallel()

	client := &fakeS3Client{}
	filestore := &s3SyncFilestore{
		provider: &s3Provider{},
		client:   client,
		bucket:   "example-bucket",
		prefix:   "prefix/",
	}

	files, err := filestore.ListFiles(context.Background())
	require.NoError(t, err)

	require.Equal(t, []string{"", "page-2"}, client.listObjectsV2Calls)
	require.Len(t, files, 2)
	require.Contains(t, files, "one.txt")
	require.Contains(t, files, "two.txt")
}
