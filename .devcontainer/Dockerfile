FROM mcr.microsoft.com/devcontainers/base:bookworm

ARG GO_VERSION=1.24.6
ARG NODE_VERSION=20
ARG USERNAME=vscode

# Install system packages
RUN apt-get update && apt-get install -y --no-install-recommends \
        jq \
        build-essential \
        maven \
        supervisor \
        ripgrep \
        apt-transport-https \
        ca-certificates \
        gnupg \
    && rm -rf /var/lib/apt/lists/*

# Install Java 21 (Temurin)
RUN wget -O - https://packages.adoptium.net/artifactory/api/gpg/key/public \
        | gpg --dearmor > /usr/share/keyrings/adoptium.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/adoptium.gpg] https://packages.adoptium.net/artifactory/deb bookworm main" \
        > /etc/apt/sources.list.d/adoptium.list \
    && apt-get update \
    && apt-get install -y temurin-21-jdk \
    && rm -rf /var/lib/apt/lists/*

# Install Go
RUN ARCH=$(dpkg --print-architecture) \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" | tar -xz -C /usr/local

ENV PATH="/usr/local/go/bin:/go/bin:${PATH}"
ENV GOPATH="/go"
RUN mkdir -p /go \
    && chown -R ${USERNAME}:${USERNAME} /go

# Install Node via NodeSource
RUN curl -fsSL https://deb.nodesource.com/setup_${NODE_VERSION}.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install kubectl
RUN curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.32/deb/Release.key \
        | gpg --dearmor -o /usr/share/keyrings/kubernetes-apt-keyring.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.32/deb/ /" \
        > /etc/apt/sources.list.d/kubernetes.list \
    && apt-get update \
    && apt-get install -y kubectl \
    && rm -rf /var/lib/apt/lists/*

# Install GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

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

# Pre-generate bash completions for all baked-in tools
RUN COMP_DIR="/etc/bash_completion.d" \
    && kubectl completion bash > "$COMP_DIR/kubectl" \
    && task --completion bash > "$COMP_DIR/task" \
    && kind completion bash > "$COMP_DIR/kind" \
    && gh completion -s bash > "$COMP_DIR/gh" \
    && kustomize completion bash > "$COMP_DIR/kustomize" \
    && npm completion > "$COMP_DIR/npm"

# Install Go dev tools
RUN go install gotest.tools/gotestsum@latest
RUN go install github.com/go-delve/delve/cmd/dlv@v1.25.1
RUN go install github.com/cosmtrek/air@v1.51.0
RUN chown -R ${USERNAME}:${USERNAME} /go


COPY supervisord.conf /etc/supervisor/conf.d/supervisord.conf

WORKDIR /home/vscode

CMD ["/usr/bin/supervisord", "-n", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
