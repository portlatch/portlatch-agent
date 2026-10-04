# SPDX-License-Identifier: Apache-2.0
#
# The release image, built by goreleaser from binaries it already compiled:
# one per platform, under $TARGETPLATFORM in the build context. Nothing runs
# on the target platform, so no emulation is needed. To build from source,
# use the Dockerfile at the root.

# The data directory, made on the builder's platform: the runtime image has no shell.
FROM --platform=$BUILDPLATFORM busybox:1.37 AS data
RUN mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/portlatch-agent /usr/local/bin/portlatch-agent
COPY --from=data --chown=65532:65532 /data /.data

# The default data directory is relative, so the working directory decides.
WORKDIR /
USER 65532:65532
VOLUME /.data

ENTRYPOINT ["/usr/local/bin/portlatch-agent"]
