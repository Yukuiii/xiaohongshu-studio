ARG BASE_IMAGE_PREFIX=docker.io/library

# ---- frontend build stage ----
FROM ${BASE_IMAGE_PREFIX}/node:24-bookworm-slim AS frontend

WORKDIR /src
COPY web/package.json web/package-lock.json ./web/
RUN npm --prefix web ci
COPY web/ ./web/
RUN npm --prefix web run build

# ---- Go build stage ----
FROM ${BASE_IMAGE_PREFIX}/golang:1.27.4 AS builder

WORKDIR /src
# 配置 Go 模块代理为国内源
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend /src/web/dist ./web/dist
# VERSION 由 CI 通过 --build-arg 传入，本地构建默认 dev
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=${VERSION}" -o /out/xiaohongshu-studio .

# ---- run stage ----
FROM ${BASE_IMAGE_PREFIX}/ubuntu:22.04

# 设置时区
ENV TZ=Asia/Shanghai
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

WORKDIR /app

# 使用已有 CA 校验证书，再通过官方 HTTPS 源安装桌面与浏览器依赖。
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ARG APT_MIRROR=https://archive.ubuntu.com/ubuntu
ARG APT_SECURITY_MIRROR=https://security.ubuntu.com/ubuntu
RUN sed -i "s|http://archive.ubuntu.com/ubuntu|${APT_MIRROR}|g; s|http://security.ubuntu.com/ubuntu|${APT_SECURITY_MIRROR}|g" /etc/apt/sources.list && \
    apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    fonts-liberation \
    fonts-noto-color-emoji \
    fonts-unifont \
    fonts-freefont-ttf \
    fonts-wqy-zenhei \
    libasound2 \
    libatk-bridge2.0-0 \
    libatk1.0-0 \
    libc6 \
    libcairo2 \
    libcairo-gobject2 \
    libcups2 \
    libdbus-1-3 \
    libdrm2 \
    libexpat1 \
    libfontconfig1 \
    libgdk-pixbuf-2.0-0 \
    libgbm1 \
    libgcc1 \
    libglib2.0-0 \
    libgtk-3-0 \
    libnspr4 \
    libnss3 \
    libpango-1.0-0 \
    libpangocairo-1.0-0 \
    libstdc++6 \
    libx11-6 \
    libx11-xcb1 \
    libxcb1 \
    libxcomposite1 \
    libxcursor1 \
    libxdamage1 \
    libxext6 \
    libxfixes3 \
    libxi6 \
    libxkbcommon0 \
    libxrandr2 \
    libxrender1 \
    libxshmfence1 \
    libxss1 \
    libxtst6 \
    lsb-release \
    tini \
    wget \
    xdg-utils \
    xz-utils \
    xvfb x11vnc novnc websockify x11-utils openbox \
    fcitx5 fcitx5-rime rime-data-luna-pinyin fcitx5-frontend-gtk3 fcitx5-module-xorg \
    dbus-x11 xsettingsd \
    && rm -rf /var/lib/apt/lists/*

# 2. 创建目录并设置权限。
RUN mkdir -p /app/data/home /app/data/config /app/images && \
    chmod -R 777 /app/data /app/images

# 3. 下载并解压内置浏览器。构建阶段预置，运行时零下载。
# 版本号唯一来源：browser/browser_version.txt（Go 也读它，避免两处漂移）。
# 从官方 fingerprint-chromium release 或同结构镜像下载，以仓库固定 SHA256 校验。
#
# 解压位置必须与 Go 端 EnsureBrowser 找的缓存路径一致：
# $XDG_CACHE_HOME/xiaohongshu-mcp/browser/<版本>/，这样运行时零下载、也不需要任何参数。
# 注意 XDG_CACHE_HOME 指向 /app/cache 而非挂载卷内，否则预置的浏览器会被挂载盖掉。
ENV XDG_CACHE_HOME=/app/cache
ARG BROWSER_DOWNLOAD_BASE=https://github.com/adryfish/fingerprint-chromium/releases/download
COPY browser/browser_version.txt /tmp/browser_version.txt
COPY browser/browser_checksums.txt /tmp/browser_checksums.txt
RUN VER="$(cat /tmp/browser_version.txt | tr -d '[:space:]')" && \
    BASE="${BROWSER_DOWNLOAD_BASE%/}/${VER}" && \
    ARCHIVE="ungoogled-chromium-${VER}-1-x86_64_linux.tar.xz" && \
    BROWSER_DIR="${XDG_CACHE_HOME}/xiaohongshu-mcp/browser/${VER}" && \
    mkdir -p "${BROWSER_DIR}" && \
    curl -fsSL -o "/tmp/${ARCHIVE}" "${BASE}/${ARCHIVE}" && \
    (cd /tmp && sha256sum --check --ignore-missing browser_checksums.txt) && \
    tar -xJf "/tmp/${ARCHIVE}" -C "${BROWSER_DIR}" && \
    rm "/tmp/${ARCHIVE}" /tmp/browser_version.txt /tmp/browser_checksums.txt && \
    test -n "$(find "${BROWSER_DIR}" -type f -name chrome -perm /111 -print -quit)" && \
    chmod -R 755 /app/cache

COPY docker/desktop/start.sh /usr/local/bin/xhs-desktop-start
COPY docker/desktop/fcitx5-profile /usr/local/share/xhs-fcitx5-profile
COPY docker/desktop/rime-default.custom.yaml /usr/local/share/xhs-rime-default.custom.yaml
RUN chmod 755 /usr/local/bin/xhs-desktop-start

COPY --from=builder /out/xiaohongshu-studio .

ENV HOME=/app/data/home
ENV XDG_CONFIG_HOME=/app/data/config
ENV XHS_ACCOUNTS_DIR=/app/data/accounts COOKIES_PATH=/app/data/cookies.json \
    XHS_DB_PATH=/app/data/studio.db
ENV DISPLAY=:99 LANG=C.UTF-8 \
    XMODIFIERS=@im=fcitx GTK_IM_MODULE=fcitx QT_IM_MODULE=fcitx \
    XHS_DESKTOP_URL=http://127.0.0.1:6080

EXPOSE 18060

# 只向主脚本转发信号，由脚本先关闭应用和浏览器，再退出桌面组件。
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/usr/local/bin/xhs-desktop-start"]

CMD ["/app/xiaohongshu-studio"]
