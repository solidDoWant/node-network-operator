# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot

ARG TARGETOS
ARG TARGETARCH

ARG SOURCE_BINARY_PATH="build/binaries/${TARGETOS}/${TARGETARCH}/node-network-operator"
ARG SOURCE_LICENSE_PATH="build/licenses/"

WORKDIR /

COPY "${SOURCE_BINARY_PATH}" /bin/node-network-operator
COPY "${SOURCE_LICENSE_PATH}" /usr/share/doc/node-network-operator

# This requires access to manage network interfaces, so run as UID 0 by default. On most systems only
# CAP_NET_ADMIN is needed, but kind seems to specifically require running as UID 0 as well
USER 0:0

ENTRYPOINT ["/bin/node-network-operator"]
