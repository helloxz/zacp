# zacp + zlite 一体化镜像
#
# 基础镜像：debian:12-slim（bookworm）
# 构建时从 GitHub Releases 拉取最新版 zacp / zlite 安装到 /usr/bin，
# 运行时以非 root 用户 app 运行（shell 智能体不给 root 权限，纵深防御），
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

# 创建基础用户 app，家目录即数据目录 /app/data（uid 固定 1000，方便挂载卷权限对齐）
RUN useradd -m -d /app/data -u 1000 app

# 安装最新版 zacp / zlite 到 /usr/bin（逻辑见 scripts/install-bins.sh）
COPY scripts/install-bins.sh /tmp/install-bins.sh
RUN bash /tmp/install-bins.sh --arch "${TARGETARCH}" \
    && rm -f /tmp/install-bins.sh

# HOME 必须显式设置：USER 指令不会改变 $HOME，
# zacp 默认按 $HOME 找状态目录（ZACP_DATA），不设会导致去写 app 无权的 /root
ENV HOME=/app/data

# 数据持久化挂载点：ZACP_DATA（默认 ~/.zacp）会落在该卷内
VOLUME ["/app/data"]

USER app
EXPOSE 8680

ENTRYPOINT ["/usr/bin/zacp"]