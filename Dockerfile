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
ARG GO_VERSION
ARG OS_CODENAME
FROM golang:1.18-buster AS builder

# Copy the sources
WORKDIR /go/src/app
COPY . ./

# Build
ARG ARCH

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=${ARCH}

RUN make kpromo

FROM gcr.io/google.com/cloudsdktool/cloud-sdk:slim AS base

WORKDIR /
COPY --from=builder /go/src/app/bin/kpromo .

# The Docker configuration file (which include credential helpers for
# authenticating to various container registries) should be placed in the home
# directory of the running user, so it can be detected by artifact promotion
# tooling.
COPY --from=builder /go/src/app/docker/config.json /root/.docker/config.json

ENTRYPOINT ["/kpromo"]

# Testing image
FROM base AS test-variant

# Include auditor testing fixtures.
COPY --from=builder /go/src/app/test-e2e/cip-auditor/fixture /e2e-fixtures

# Trigger the auditor on startup.
ENTRYPOINT ["/kpromo", "cip", "audit", "--verbose"]

# Production image
FROM base AS prod-variant

LABEL maintainers="Kubernetes Authors"
LABEL description="kpromo: The Kubernetes project artifact promoter"

# Allow the runtime argument to choose the final variant.
FROM ${variant}-variant AS final
