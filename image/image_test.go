/*
Copyright 2021 The Kubernetes Authors.

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

package image

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewManifestListFromFile(t *testing.T) {
	listYAML := "- name: pause\n"
	listYAML += "  dmap:\n"
	listYAML += "    \"sha256:927d98197ec1141a368550822d18fa1c60bdae27b78b0c004f705f548c07814f\": [\"3.2\"]\n"
	listYAML += "    \"sha256:a319ac2280eb7e3a59e252e54b76327cb4a33cf8389053b0d78277f22bbca2fa\": [\"3.3\"]\n"

	tempFile, err := os.CreateTemp("", "release-test")
	require.Nil(t, err, "creating temp file")
	defer os.Remove(tempFile.Name())

	_, err = tempFile.Write([]byte(listYAML))
	require.Nil(t, err, "wrinting temporary promoter image list")

	imageList, err := NewManifestListFromFile(tempFile.Name())
	require.Nil(t, err)

	require.Equal(t, 1, len(*imageList))
	require.Equal(t, 2, len((*imageList)[0].DMap))
}

func TestPromoterImageParse(t *testing.T) {
	listYAML := "- name: kube-apiserver-amd64\n  dmap:\n"
	listYAML += "    \"sha256:365063a9b0df28cb8b72525138214079085ce8376e47a8654e34d16766c432f9\": [\"v1.18.9-rc.0\"]\n"
	listYAML += "    \"sha256:3c65dfd9682ca03989ac8ae9db230ea908e2ba00a8db002b33b09ca577f5c05c\": [\"v1.19.2-rc.0\"]\n"
	listYAML += "    \"sha256:43374266764aee719ce342c3611d34a12c68315a64a4197a2571b7434bb42f82\": [\"v1.19.1\"]\n"
	listYAML += "    \"sha256:4da7d4a9176971d2af0a5e4bd6f764677648db4ad2814574fbb76962458c7bbb\": [\"v1.19.0-rc.2\"]\n"
	listYAML += "    \"sha256:4fd1a6d25b5fe5db3647ed1d368c671b618efafb6ddbe06fc64697d2ba271aa9\": [\"v1.18.8\"]\n"
	listYAML += "    \"sha256:5b6b95cc8c06262719d10149964ca59496b234e28ef3e3fcdf7323f46c83ce04\": [\"v1.19.0-rc.4\"]\n"
	listYAML += "    \"sha256:6257f45b4908eed0a4b84d8efeaf2751096ce516006daf74690b321b785e6cc4\": [\"v1.19.0\"]\n"
	listYAML += "- name: pause\n  dmap:\n"
	listYAML += "    \"sha256:927d98197ec1141a368550822d18fa1c60bdae27b78b0c004f705f548c07814f\": [\"3.2\"]\n"
	listYAML += "    \"sha256:a319ac2280eb7e3a59e252e54b76327cb4a33cf8389053b0d78277f22bbca2fa\": [\"3.3\"]\n"

	imageList := &ManifestList{}
	err := imageList.Parse([]byte(listYAML))
	require.Nil(t, err, "parsing image list yaml")

	require.Equal(t, 2, len(*imageList))
	require.Equal(t, 7, len((*imageList)[0].DMap))
	require.Equal(t, 2, len((*imageList)[1].DMap))
	require.Equal(t, "kube-apiserver-amd64", (*imageList)[0].Name)
	require.Equal(t, "pause", (*imageList)[1].Name)
	require.Equal(t, "v1.19.0", (*imageList)[0].DMap["sha256:6257f45b4908eed0a4b84d8efeaf2751096ce516006daf74690b321b785e6cc4"][0])
	require.Equal(t, "3.3", (*imageList)[1].DMap["sha256:a319ac2280eb7e3a59e252e54b76327cb4a33cf8389053b0d78277f22bbca2fa"][0])
}

func TestPromoterImageToYAML(t *testing.T) {
	imageList := &ManifestList{
		struct {
			Name string              "json:\"name\""
			DMap map[string][]string "json:\"dmap\""
		}{
			Name: "hyperkube",
			DMap: map[string][]string{
				"sha256:54cdd8d3b74f9c577c8bb4f43e50813f0190006e66efe861bd810ee3f5e7cc7d": {"v1.18.8"},
				"sha256:03427dcf5ab5fc5fd3cdfb24170373e8afbed13356270666c823573d7e2a1342": {"v1.16.16-rc.0"},
				"sha256:9f35b65ee834239ffbbd0ddfb54e0317cf99f10a75d8e8af372af45286d069ab": {"v1.17.10"},
			},
		},
		struct {
			Name string              "json:\"name\""
			DMap map[string][]string "json:\"dmap\""
		}{
			Name: "conformance",
			DMap: map[string][]string{
				"sha256:17fcac56c871a58a093ff36915816161b1dbbb9eca0add9c968d9c27c4ba1881": {"v1.19.0"},
			},
		},
		struct {
			Name string              "json:\"name\""
			DMap map[string][]string "json:\"dmap\""
		}{
			Name: "kube-proxy",
			DMap: map[string][]string{
				"sha256:c752ecbd04bc4517168a19323bb60fb45324eee1e480b2b97d3fd6ea0a54f42d": {"v1.18.8", "v1.19.0"},
			},
		},
	}

	// Expected yaml output, must be sorted correctly, according to the image promoter sort order
	expectedYAML := "- name: conformance\n  dmap:\n"
	expectedYAML += "    \"sha256:17fcac56c871a58a093ff36915816161b1dbbb9eca0add9c968d9c27c4ba1881\": [\"v1.19.0\"]\n"
	expectedYAML += "- name: hyperkube\n  dmap:\n"
	expectedYAML += "    \"sha256:03427dcf5ab5fc5fd3cdfb24170373e8afbed13356270666c823573d7e2a1342\": [\"v1.16.16-rc.0\"]\n"
	expectedYAML += "    \"sha256:54cdd8d3b74f9c577c8bb4f43e50813f0190006e66efe861bd810ee3f5e7cc7d\": [\"v1.18.8\"]\n"
	expectedYAML += "    \"sha256:9f35b65ee834239ffbbd0ddfb54e0317cf99f10a75d8e8af372af45286d069ab\": [\"v1.17.10\"]\n"
	expectedYAML += "- name: kube-proxy\n  dmap:\n"
	expectedYAML += "    \"sha256:c752ecbd04bc4517168a19323bb60fb45324eee1e480b2b97d3fd6ea0a54f42d\": [\"v1.18.8\",\"v1.19.0\"]\n"

	yamlCode, err := imageList.ToYAML()
	require.Nil(t, err, "serilizing imagelist to yaml")
	require.Equal(t, expectedYAML, string(yamlCode), "checking promoter image list yaml output")
}

func TestPromoterImageWrite(t *testing.T) {
	imageList := &ManifestList{
		struct {
			Name string              "json:\"name\""
			DMap map[string][]string "json:\"dmap\""
		}{
			Name: "kube-controller-manager-s390x",
			DMap: map[string][]string{
				"sha256:594b8333e79ecca96c9ff0cb72a001db181c199d83274ffbe5ccdaedca23bfd7": {"v1.19.1"},
			},
		},
		struct {
			Name string              "json:\"name\""
			DMap map[string][]string "json:\"dmap\""
		}{
			Name: "kube-scheduler",
			DMap: map[string][]string{
				"sha256:022b81d70447014f63fdc734df48cb9e3a2854c48f65acdca67aac5c1974fc22": {"v1.19.0-rc.2"},
			},
		},
	}

	expectedFile := "- name: kube-controller-manager-s390x\n  dmap:\n"
	expectedFile += "    \"sha256:594b8333e79ecca96c9ff0cb72a001db181c199d83274ffbe5ccdaedca23bfd7\": [\"v1.19.1\"]\n"
	expectedFile += "- name: kube-scheduler\n  dmap:\n"
	expectedFile += "    \"sha256:022b81d70447014f63fdc734df48cb9e3a2854c48f65acdca67aac5c1974fc22\": [\"v1.19.0-rc.2\"]\n"

	tempFile, err := os.CreateTemp("", "release-test")
	require.Nil(t, err, "creating temp file")
	defer os.Remove(tempFile.Name())

	err = imageList.Write(tempFile.Name())
	require.Nil(t, err, "writing data to disk")

	// Read back the file to see if it correct
	fileContents, err := os.ReadFile(tempFile.Name())
	require.Nil(t, err, "reading temporary file")

	require.Equal(t, expectedFile, string(fileContents))
}
