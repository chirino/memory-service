#!/bin/bash
set -e

USER_HOME="${HOME:-/home/vscode}"

# Link to the ~/.m2 dir
if [ -d "/home/vscode/.m2" ]; then
  ln -s /home/vscode/.m2 "$USER_HOME/.m2"
fi

# Generate bash completion for docker (installed by docker-in-docker feature, not baked in)
docker completion bash | sudo tee /etc/bash_completion.d/docker > /dev/null 2>/dev/null || true
