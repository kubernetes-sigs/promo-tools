# Copyright 2021 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# About: This dockerfile builds the cip binary for auditor tests and production use.
#
# Usage: Since there are two variants to build, you must include the variant name during build-time.
# cip production binary: 
#   docker build --build-arg variant=prod /path/to/Dockerfile
# test auditor:
#   docker build --build-arg variant=test /path/to/Dockerfile

# Determine final build variant [prod | test].
ARG variant

FROM golang:latest AS base
# Transfer all project files to container.
WORKDIR /go/src/app
COPY . .
# Build and export cip command.
RUN ./go_with_version.sh build ./cmd/cip
RUN cp ./cip /bin/cip
# Provide docker config for repo information.
RUN mkdir /.docker
RUN cp ./docker/config.json /.docker/config.json

FROM gcr.io/google.com/cloudsdktool/cloud-sdk:latest AS gcloud-base
COPY --from=base / /

FROM base AS test-variant
# Include cip-auditor testing fixtures.
RUN mkdir /e2e-fixtures
RUN cp -r ./test-e2e/cip-auditor/fixture/* /e2e-fixtures
# Trigger the auditor on startup.
ENV HOME=/
ENTRYPOINT ["cip", "audit", "--verbose"]

FROM gcloud-base as prod-variant
ENV HOME=/
ENTRYPOINT ["/bin/bash", "-c"]

# Allow the runtime argument to choose the final variant.
FROM ${variant}-variant AS final
