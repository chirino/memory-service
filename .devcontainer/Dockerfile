FROM mcr.microsoft.com/devcontainers/java:21-bookworm

# Install system packages
RUN apt-get update && apt-get install -y --no-install-recommends \
        jq \
        build-essential \
        maven \
        supervisor \
    && rm -rf /var/lib/apt/lists/*

COPY supervisord.conf /etc/supervisor/conf.d/supervisord.conf

# Build microsocks (lightweight SOCKS5 proxy with remote DNS support)
RUN git clone https://github.com/rofl0r/microsocks.git /tmp/microsocks \
    && cd /tmp/microsocks \
    && make \
    && cp microsocks /usr/local/bin/ \
    && rm -rf /tmp/microsocks

# Install go-task (https://taskfile.dev)
RUN sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin

# Install kind
RUN ARCH=$(dpkg --print-architecture) \
    && curl -Lo /usr/local/bin/kind "https://kind.sigs.k8s.io/dl/latest/kind-linux-${ARCH}" \
    && chmod +x /usr/local/bin/kind

# Install kustomize
RUN cd /tmp \
    && curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash \
    && mv kustomize /usr/local/bin/

# Install grpcurl
RUN ARCH=$(dpkg --print-architecture) \
    && GRPCURL_ARCH=$([ "${ARCH}" = "arm64" ] && echo "arm64" || echo "x86_64") \
    && GRPCURL_VERSION=$(curl -s https://api.github.com/repos/fullstorydev/grpcurl/releases/latest | jq -r .tag_name) \
    && curl -sSL "https://github.com/fullstorydev/grpcurl/releases/download/${GRPCURL_VERSION}/grpcurl_${GRPCURL_VERSION#v}_linux_${GRPCURL_ARCH}.tar.gz" \
       | tar -xz -C /usr/local/bin grpcurl

# Install uv (Python package/project manager)
RUN curl -LsSf https://astral.sh/uv/install.sh | sh \
    && install -m 0755 /root/.local/bin/uv /usr/local/bin/uv \
    && install -m 0755 /root/.local/bin/uvx /usr/local/bin/uvx

CMD ["/usr/bin/supervisord", "-n", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
