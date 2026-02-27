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

# Generate bash completion for docker (installed by docker-in-docker feature, not baked in)
docker completion bash | sudo tee /etc/bash_completion.d/docker > /dev/null 2>/dev/null || true
