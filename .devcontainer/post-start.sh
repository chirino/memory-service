#!/bin/bash
# Copy host user config into the container user's home.
# /home/host is a read-only bind mount of the host user's home directory.
set -e

HOST_HOME="/home/host"
USER_HOME="${HOME:-/home/vscode}"

for item in .ssh .gitconfig; do
  src="$HOST_HOME/$item"
  dest="$USER_HOME/$item"
  if [ -e "$src" ]; then
    rm -rf "$dest"
    cp -a "$src" "$dest"
  fi
done

# Fix SSH directory permissions (copies need restrictive perms)
if [ -d "$USER_HOME/.ssh" ]; then
  chmod 700 "$USER_HOME/.ssh"
  chmod 600 "$USER_HOME/.ssh"/* 2>/dev/null || true
  chmod 644 "$USER_HOME/.ssh"/*.pub 2>/dev/null || true
  chmod 644 "$USER_HOME/.ssh/known_hosts" 2>/dev/null || true
  chmod 644 "$USER_HOME/.ssh/config" 2>/dev/null || true
fi

# Link to the ~/.m2 dir
if [ -d "/home/vscode/.m2" ]; then
  ln -s /home/vscode/.m2 "$USER_HOME/.m2"
fi

# Generate bash completions for tools installed by devcontainer features
COMP_DIR="/etc/bash_completion.d"
kubectl completion bash | sudo tee "$COMP_DIR/kubectl" > /dev/null 2>/dev/null || true
task --completion bash | sudo tee "$COMP_DIR/task" > /dev/null 2>/dev/null || true
kind completion bash | sudo tee "$COMP_DIR/kind" > /dev/null 2>/dev/null || true
gh completion -s bash | sudo tee "$COMP_DIR/gh" > /dev/null 2>/dev/null || true
kustomize completion bash | sudo tee "$COMP_DIR/kustomize" > /dev/null 2>/dev/null || true
docker completion bash | sudo tee "$COMP_DIR/docker" > /dev/null 2>/dev/null || true
npm completion | sudo tee "$COMP_DIR/npm" > /dev/null 2>/dev/null || true

go install gotest.tools/gotestsum@latest
