#!/usr/bin/env bash
# Publishes and stages the native-binary OCI artifact that memory-service-process downloads its
# executable from. See docs/enhancements/implemented/119-oci-native-binary-distribution.md.
#
# Usage:
#   binary-oci.sh push <image-ref> <version> <binaries-dir>
#       Gzips <binaries-dir>/<platform>/memory-service for each platform and pushes one manifest
#       with a layer per platform. Does nothing when the tag already exists: published JARs pin
#       layer digests, so a tag is never overwritten.
#   binary-oci.sh stage <image-ref> <descriptor-repository> <out-dir>
#       Pulls the manifest and every layer, verifies each layer digest and size, and writes
#       <out-dir>/<platform>/{memory-service, memory-service.sha256, memory-service.oci.properties}.
#       The descriptor names <descriptor-repository>, which can differ from where it was pulled
#       (CI pulls from a local registry but describes ghcr.io).
#
# The image ref must include a tag, for example ghcr.io/chirino/memory-service-binary:1.2.3.
# Set ORAS_PLAIN_HTTP=true for a plain-HTTP registry such as a CI registry:2 container.
# Set ORAS_ANONYMOUS=true to ignore stored registry credentials, which proves the artifact is
# publicly readable.
set -euo pipefail

PLATFORMS=(linux-amd64 linux-arm64 macos-arm64)
ARTIFACT_TYPE=application/vnd.io.github.chirino.memory-service.binaries.v1
LAYER_TYPE=application/vnd.io.github.chirino.memory-service.binary.v1+gzip
SOURCE=https://github.com/chirino/memory-service

oras_args=()
if [ "${ORAS_PLAIN_HTTP:-false}" = "true" ]; then
  oras_args+=(--plain-http)
fi
if [ "${ORAS_ANONYMOUS:-false}" = "true" ]; then
  anonymous_config="$(mktemp)"
  trap 'rm -f "$anonymous_config"' EXIT
  echo '{"auths":{}}' >"$anonymous_config"
  oras_args+=(--registry-config "$anonymous_config")
fi

die() {
  echo "binary-oci: $*" >&2
  exit 1
}

oras_cmd() {
  oras "$@" ${oras_args[@]+"${oras_args[@]}"}
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

size_of() {
  wc -c <"$1" | tr -d ' '
}

require_tag() {
  local ref="$1"
  case "${ref##*/}" in
    *:*) ;;
    *) die "image ref must include a tag: $ref" ;;
  esac
}

# Exit status 0 when the tag exists, 1 when the registry reports it missing. Any other error is
# fatal, so a registry or auth failure never leads to an overwrite.
tag_exists() {
  local ref="$1" output
  if output="$(oras_cmd manifest fetch --descriptor "$ref" 2>&1)"; then
    return 0
  fi
  if grep -qiE 'not found|manifest unknown|MANIFEST_UNKNOWN' <<<"$output"; then
    return 1
  fi
  echo "$output" >&2
  die "could not check whether $ref exists"
}

push() {
  [ "$#" -eq 3 ] || die "usage: binary-oci.sh push <image-ref> <version> <binaries-dir>"
  local ref="$1" version="$2" binaries="$3"
  require_tag "$ref"
  if tag_exists "$ref"; then
    echo "$ref already exists; leaving it unchanged"
    return 0
  fi

  local work files=() platform source
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' RETURN
  for platform in "${PLATFORMS[@]}"; do
    source="$binaries/$platform/memory-service"
    [ -f "$source" ] || die "missing $source"
    gzip -n -9 -c "$source" >"$work/memory-service-$platform.gz"
    files+=("memory-service-$platform.gz:$LAYER_TYPE")
  done

  (
    cd "$work"
    oras_cmd push \
      --artifact-type "$ARTIFACT_TYPE" \
      --annotation "org.opencontainers.image.source=$SOURCE" \
      --annotation "org.opencontainers.image.version=$version" \
      "$ref" "${files[@]}"
  )
}

stage() {
  [ "$#" -eq 3 ] || die "usage: binary-oci.sh stage <image-ref> <descriptor-repository> <out-dir>"
  local ref="$1" repository="$2" out="$3"
  require_tag "$ref"
  local reference="${ref##*:}" name="${ref%:*}"

  local work manifest
  work="$(mktemp -d)"
  trap 'rm -rf "$work"' RETURN
  manifest="$(oras_cmd manifest fetch "$ref")"
  [ "$(jq -r '.artifactType // ""' <<<"$manifest")" = "$ARTIFACT_TYPE" ] \
    || die "$ref is not a $ARTIFACT_TYPE artifact"

  local platform title layer digest size media blob target binary_sha binary_size
  for platform in "${PLATFORMS[@]}"; do
    title="memory-service-$platform.gz"
    layer="$(jq -c --arg title "$title" \
      '[.layers[] | select(.annotations["org.opencontainers.image.title"] == $title)]' \
      <<<"$manifest")"
    [ "$(jq 'length' <<<"$layer")" -eq 1 ] || die "$ref has no single layer titled $title"
    digest="$(jq -r '.[0].digest' <<<"$layer")"
    size="$(jq -r '.[0].size' <<<"$layer")"
    media="$(jq -r '.[0].mediaType' <<<"$layer")"
    [ "$media" = "$LAYER_TYPE" ] || die "$title has media type $media"
    [[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "$title has unsupported digest $digest"

    blob="$work/$title"
    oras_cmd blob fetch --output "$blob" "$name@$digest"
    [ "sha256:$(sha256_of "$blob")" = "$digest" ] || die "$title digest mismatch"
    [ "$(size_of "$blob")" = "$size" ] || die "$title size mismatch"

    target="$out/$platform"
    mkdir -p "$target"
    gzip -d -c "$blob" >"$target/memory-service"
    chmod +x "$target/memory-service"
    binary_sha="$(sha256_of "$target/memory-service")"
    binary_size="$(size_of "$target/memory-service")"
    printf '%s  memory-service\n' "$binary_sha" >"$target/memory-service.sha256"
    {
      echo "repository=$repository"
      echo "reference=$reference"
      echo "layerDigest=$digest"
      echo "layerSize=$size"
      echo "binarySize=$binary_size"
    } >"$target/memory-service.oci.properties"
    echo "Staged $platform from $name@$digest"
  done
}

command="${1:-}"
shift || true
case "$command" in
  push) push "$@" ;;
  stage) stage "$@" ;;
  *) die "usage: binary-oci.sh push|stage ..." ;;
esac
