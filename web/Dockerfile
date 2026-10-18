# syntax=docker/dockerfile:1

# Builds the Svelte SPA and serves the static bundle with nginx.
# The bundle is platform-independent: build it natively, only nginx is per-arch.
FROM --platform=$BUILDPLATFORM node:22-alpine AS build
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

FROM nginx:1.27-alpine
COPY conf.d-upgrade-map.conf /etc/nginx/conf.d/00-upgrade-map.conf
# templates/*.template are filled in from the environment at startup.
COPY templates/ /etc/nginx/templates/
ENV EVRD_API=evrd-api:8098 NGINX_ENTRYPOINT_LOCAL_RESOLVERS=1
COPY --from=build /app/build /usr/share/nginx/html
EXPOSE 80
