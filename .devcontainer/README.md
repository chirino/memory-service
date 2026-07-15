# Devcontainer Security Notes

This devcontainer is intended for trusted workspace development only.

- It uses the official Docker-in-Docker feature with an isolated inner Docker daemon.
- It does not mount the host Docker socket.
- It does not mount the host home directory or copy host SSH keys into the container.
- It does not start a SOCKS proxy or publish port 1080.

Use editor SSH-agent forwarding for Git operations that require private keys. Do not open
untrusted repositories, branches, or generated code in this privileged devcontainer.
