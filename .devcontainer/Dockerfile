FROM mcr.microsoft.com/devcontainers/go:1-1.24-bookworm

# kubebuilder
RUN curl -fsSL -o /usr/local/bin/kubebuilder "https://github.com/kubernetes-sigs/kubebuilder/releases/download/v4.6.0/kubebuilder_$(go env GOOS)_$(go env GOARCH)" && \
    chmod +x /usr/local/bin/kubebuilder

# Tools
RUN apt update && apt install --no-install-recommends -y inetutils-ping tcpdump
