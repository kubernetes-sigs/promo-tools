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

# About: This dockerfile builds the kpromo binary for auditor tests and production use.
#
# Usage: Since there are two variants to build, you must include the variant name during build-time.
# kpromo production binary:
#   docker build --build-arg variant=prod /path/to/Dockerfile
# test auditor:
#   docker build --build-arg variant=test /path/to/Dockerfile

# Determine final build variant [prod | test].
ARG variant

# Base image
FROM golang:1.17-buster AS base

# Transfer all project files to container.
WORKDIR /go/src/app
COPY . .

# Build and export kpromo command.
RUN ./go_with_version.sh build ./cmd/kpromo
RUN cp ./kpromo /bin/kpromo

# Provide docker config for repo information.
RUN mkdir /.docker
RUN cp ./docker/config.json /.docker/config.json

# gcloud-base image
FROM gcr.io/google.com/cloudsdktool/cloud-sdk:slim AS gcloud-base

COPY --from=base / /

# Testing image
FROM base AS test-variant

# Include cip-auditor testing fixtures.
RUN mkdir /e2e-fixtures
RUN cp -r ./test-e2e/cip-auditor/fixture/* /e2e-fixtures

# Trigger the auditor on startup.
ENV HOME=/
ENTRYPOINT ["kpromo", "cip", "audit", "--verbose"]

# Production image
FROM gcloud-base as prod-variant

ENV HOME=/
ENTRYPOINT ["/bin/bash", "-c"]

# Allow the runtime argument to choose the final variant.
FROM ${variant}-variant AS final
