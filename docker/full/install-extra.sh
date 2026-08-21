#!/usr/bin/env bash
# scripts/install-extra.sh — full 镜像专用：安装 lite 之外的额外工具
#
# 由 docker/full/Dockerfile 以 root 身份调用：bash /tmp/install-extra.sh
# （zacp / zlite 由 scripts/install-bins.sh 安装；本脚本只负责 full 镜像多出的工具）
set -euo pipefail

echo "==> Installing OfficeCLI (iOfficeAI)..."
curl -fsSL https://raw.githubusercontent.com/iOfficeAI/OfficeCLI/main/install.sh | bash

echo "==> install-extra.sh complete."
