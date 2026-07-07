# Development image for an Angular 21 project.
# Angular 21 requires Node.js ^20.19, ^22.12 or >=24 — node:22 (LTS) covers it.
FROM node:22-alpine

WORKDIR /app

# Install dependencies in their own layer so they are only re-installed
# when package.json / package-lock.json actually change.
COPY package.json package-lock.json ./
RUN npm ci

# Copy the rest of the project. In day-to-day dev this layer is shadowed
# by the bind mount from docker-compose.yml, but it keeps the image
# runnable on its own (docker run) too.
COPY . .

# 4200: Angular dev server (HTTP + Vite HMR websocket on the same port)
EXPOSE 4200

# --host 0.0.0.0 so the server is reachable from outside the container.
# --poll 2000 because inotify events don't cross the bind-mount boundary
# on macOS/Windows; polling makes live reload work everywhere.
CMD ["npx", "ng", "serve", "--host", "0.0.0.0", "--port", "4200", "--poll", "2000"]
