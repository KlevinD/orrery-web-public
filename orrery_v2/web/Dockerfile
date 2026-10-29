ARG NODE_VERSION
ARG NPM_REGISTRY
ARG APT_MIRROR
ARG DEBIAN_SECURITY_MIRROR

FROM node:${NODE_VERSION}-bookworm-slim AS build

ARG NPM_REGISTRY

WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
COPY runtime/orrery-js /src/runtime/orrery-js
RUN npm ci --registry="${NPM_REGISTRY}"

COPY web/ ./
ARG VITE_API_BASE_URL
ENV VITE_API_BASE_URL=${VITE_API_BASE_URL}
RUN npm run build

FROM nginx:stable-bookworm

ARG APT_MIRROR
ARG DEBIAN_SECURITY_MIRROR

RUN printf 'deb %s bookworm main contrib non-free non-free-firmware\n\
deb %s bookworm-updates main contrib non-free non-free-firmware\n\
deb %s bookworm-security main contrib non-free non-free-firmware\n' \
        "${APT_MIRROR}" "${APT_MIRROR}" "${DEBIAN_SECURITY_MIRROR}" > /etc/apt/sources.list.d/debian.sources.list \
    && rm -f /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*

COPY web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/web/build /usr/share/nginx/html
COPY --from=build /src/web/build/_app/immutable /opt/orrery-web-immutable
COPY web/docker-entrypoint.d/40-sync-immutable-assets.sh /docker-entrypoint.d/40-sync-immutable-assets.sh
RUN chmod 0755 /docker-entrypoint.d/40-sync-immutable-assets.sh

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=5 \
    CMD curl -fsS http://127.0.0.1/ >/dev/null || exit 1
