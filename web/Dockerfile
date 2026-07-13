# syntax=docker/dockerfile:1.6

# ---- Build stage ----
FROM node:20-alpine AS build

WORKDIR /app

# Install deps (cached layer when only source changes)
COPY package.json package-lock.json* ./
RUN npm ci || npm install

# Build
COPY . .
RUN npm run build

# ---- Runtime stage ----
FROM nginx:alpine AS runtime

# SPA fallback + static asset cache
COPY nginx.conf /etc/nginx/conf.d/default.conf

# Built static files
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80

# Healthcheck — curl may not be present in nginx:alpine by default
HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -q -O- http://127.0.0.1/ >/dev/null 2>&1 || exit 1

CMD ["nginx", "-g", "daemon off;"]
