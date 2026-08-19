# zacp + zlite 一体化镜像（精简版）
#
# 构建（本目录即 build context，脚本已随镜像放在同级）：
#   cd docker/lite && docker build -t zacp:lite .
#
# 基础镜像：debian:12-slim（bookworm）
# 构建时从 GitHub Releases 拉取最新版 zacp / zlite 安装到 /usr/bin。
# 运行时以 root 用户运行（省去挂载目录属主治理，bind mount 不受宿主权限限制）。
# 数据目录 /app/data（VOLUME 提供持久化，config.toml 与数据库都在其中）。
FROM debian:12-slim

# TARGETARCH 由 docker buildx 自动注入（amd64/arm64），用于选择 release 包；
# 非 buildx 构建时为空，脚本会回退到 uname -m 自动检测
ARG TARGETARCH

# 基础依赖：curl 用于构建期下载、ca-certificates 用于 GitHub HTTPS 证书校验；
# curl 保留在最终镜像中——运行时 zlite 智能体执行 shell 命令时也会用到
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# 安装最新版 zacp / zlite 到 /usr/bin（脚本为同级 install-bins.sh，同步自 scripts/install-bins.sh）
COPY install-bins.sh /tmp/install-bins.sh
RUN bash /tmp/install-bins.sh --arch "${TARGETARCH}" \
    && rm -f /tmp/install-bins.sh

# HOME 必须显式设置：容器默认 HOME=/root，
# zacp 按 $HOME 找状态目录（ZACP_DATA），设为 /app/data 使数据落在挂载卷内
# PATH 追加 /root/.local/bin：有些工具(如部分 CLI 安装器)会装到该目录，需纳入可执行搜索路径
ENV HOME=/app/data \
    PATH="/root/.local/bin:${PATH}"

# 数据持久化挂载点：ZACP_DATA（默认 ~/.zacp）会落在该卷内
VOLUME ["/app/data"]

EXPOSE 8680

ENTRYPOINT ["/usr/bin/zacp"]