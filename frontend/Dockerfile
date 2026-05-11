# syntax=docker/dockerfile:1.7

# --- Build stage --------------------------------------------------------
FROM node:26-alpine AS builder

WORKDIR /app

# Install deps with cache-friendly lockfile step
COPY package.json package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm \
    npm install --no-audit --no-fund

COPY . .

RUN npm run build

# --- Runtime stage: nginx ----------------------------------------------
FROM nginx:1.27-alpine AS runtime

# Drop nginx default config and supply our own
COPY nginx/default.conf /etc/nginx/conf.d/default.conf

# Copy the built static assets
COPY --from=builder /app/dist /usr/share/nginx/html

# Run nginx as non-root
RUN adduser -S -u 10002 -G nginx marauder \
 && chown -R marauder:nginx /usr/share/nginx/html /var/cache/nginx /var/run /var/log/nginx \
 && touch /var/run/nginx.pid \
 && chown marauder:nginx /var/run/nginx.pid

USER marauder:nginx
EXPOSE 8081

HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:8081/ >/dev/null 2>&1 || exit 1

CMD ["nginx", "-g", "daemon off;"]
