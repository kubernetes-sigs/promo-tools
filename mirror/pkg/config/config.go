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

package config

import (
	"io/ioutil"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultAWSBucketPrefix is the default prefix for buckets being mirrored to
	DefaultAWSBucketPrefix = "test-"
)

// Config stores configuration options for the mirror tool
type Config struct {
	AWS *AWSConfig `json:"aws,omitempty"`
}

// AWSConfig stores information about the AWS mirror configurations
type AWSConfig struct {
	// Regions is the set of AWS regions that will have objects mirrored to it
	Regions []string `json:"regions"`
	// BucketPrefix is a string prefix for the names of S3 Buckets that will be
	// written to. Useful for doing dry-run testing.",
	BucketPrefix string `json:"bucket_prefix,omitempty"`
}

// New returns an empty AWS-specific Config
func New() *Config {
	return &Config{
		AWS: &AWSConfig{
			BucketPrefix: DefaultAWSBucketPrefix,
		},
	}
}

// FromFile returns an AWS-specific Config given a path to a YAML file
// containing configuration options
func FromFile(fp string) (*Config, error) {
	f, err := ioutil.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	c := Config{}
	err = yaml.Unmarshal(f, &c)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
