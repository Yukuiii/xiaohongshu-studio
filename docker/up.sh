#!/usr/bin/env bash
set -euo pipefail

# 用提交号标记本地镜像，未提交改动加 -dirty，避免把旧镜像误当成当前源码。
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tag="${XHS_IMAGE_TAG:-$(git -C "$script_dir" rev-parse --short=12 HEAD)}"
if [[ -n "$(git -C "$script_dir" status --porcelain --untracked-files=no)" ]]; then
    tag="${tag}-dirty"
fi
export XHS_IMAGE_TAG="$tag"

image="ghcr.io/yukuiii/xiaohongshu-studio:${tag}"
echo "构建并启动 ${image}"
exec docker compose -f "$script_dir/docker-compose.yml" up -d --build "$@"
