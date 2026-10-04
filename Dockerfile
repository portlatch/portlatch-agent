# SPDX-License-Identifier: Apache-2.0

# The build stage runs on the builder's own platform and cross-compiles: no
# emulation, whatever the target.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG API_BASE_URL=""
ARG TARGETOS TARGETARCH TARGETVARIANT
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} go build \
        -trimpath \
        -ldflags "-s -w ${API_BASE_URL:+-X github.com/portlatch/portlatch-agent/internal/agent.defaultAPIBaseURL=$API_BASE_URL}" \
        -o /out/portlatch-agent ./cmd/portlatch-agent

# The data directory is created here because the runtime image has no shell.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/portlatch-agent /usr/local/bin/portlatch-agent
COPY --from=build --chown=65532:65532 /out/data /.data

# The default data directory is relative, so the working directory decides.
WORKDIR /
USER 65532:65532
VOLUME /.data

ENTRYPOINT ["/usr/local/bin/portlatch-agent"]
