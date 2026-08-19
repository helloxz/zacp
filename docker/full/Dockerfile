# zacp + zlite 全功能镜像（含 node / npm / python 等开发环境工具链）
#
# 构建（本目录即 build context，脚本已随镜像放在同级）：
#   cd docker/full && docker build -t zacp:full .
#
# 基础镜像：node:slim（基于 Debian slim，自带 node/npm、python3 等）；
# 注意：node:slim 官方构建会把 curl 等 apt 工具 purge 掉，并不自带 curl，
# 故与 lite 一样需显式安装 curl/ca-certificates。
# 运行时以 root 用户运行；数据目录 /app/data（VOLUME 提供持久化）。
FROM node:slim

# TARGETARCH 由 docker buildx 自动注入（amd64/arm64），用于选择 release 包；
# 非 buildx 构建时为空，脚本会回退到 uname -m 自动检测
ARG TARGETARCH

# 基础依赖：curl 用于下载、ca-certificates 用于 GitHub HTTPS 证书校验；
# 二者也保留在最终镜像，供运行时 zlite 智能体执行 shell 命令时使用
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# 安装最新版 zacp / zlite（脚本为同级 install-bins.sh，同步自 scripts/install-bins.sh）
COPY install-bins.sh /tmp/install-bins.sh
RUN bash /tmp/install-bins.sh --arch "${TARGETARCH}" \
    && rm -f /tmp/install-bins.sh

# 安装额外工具链：@wecom/cli 与 OfficeCLI（脚本为同级 install-extra.sh，同步自 scripts/install-extra.sh）
COPY install-extra.sh /tmp/install-extra.sh
RUN bash /tmp/install-extra.sh \
    && rm -f /tmp/install-extra.sh

# HOME 必须显式设置：容器默认 HOME=/root，
# zacp 按 $HOME 找状态目录（ZACP_DATA），设为 /app/data 使数据落在挂载卷内
ENV HOME=/app/data

# 数据持久化挂载点：ZACP_DATA（默认 ~/.zacp）会落在该卷内
VOLUME ["/app/data"]

EXPOSE 8680

ENTRYPOINT ["/usr/bin/zacp"]