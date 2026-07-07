# Angular 21 — Docker dev environment

A drop-in Docker setup for developing an Angular 21 app locally with live
reload, plus an IntelliJ run configuration for debugging at
`http://localhost:4200`.

## What's in here

| File | Purpose |
|------|---------|
| `Dockerfile` | Dev image: Node 22 (LTS), `npm ci`, runs `ng serve` |
| `docker-compose.yml` | Binds your source into the container, maps port 4200 |
| `.dockerignore` | Keeps `node_modules`, `dist`, `.angular` etc. out of the build context |
| `.run/Debug Angular (localhost 4200).run.xml` | Ready-made IntelliJ "JavaScript Debug" run configuration |

## Setup

Copy the contents of this directory into the **root of your Angular
project** (next to `package.json` and `angular.json`), including the
hidden `.dockerignore` file and `.run/` directory.

Then start the dev server:

```bash
docker compose up --build
```

First build installs dependencies and takes a while; subsequent starts are
fast. The app is served at **http://localhost:4200**.

Edit files on your host as usual — the source directory is bind-mounted
into the container, so the Angular dev server rebuilds and hot-reloads the
browser automatically. File watching uses polling (`--poll 2000`) because
inotify events don't cross the Docker bind-mount boundary on macOS and
Windows.

### When dependencies change

`node_modules` lives in a container-side volume (not on your host), so
after changing `package.json`:

```bash
docker compose up --build
```

Or without rebuilding the image:

```bash
docker compose exec angular npm install
```

## Debugging in IntelliJ

Client-side Angular code runs in your **browser**, not in Node, so the
debugger attaches to Chrome via a *JavaScript Debug* run configuration —
IntelliJ IDEA Ultimate (or WebStorm) is required for this.

The `.run/Debug Angular (localhost 4200).run.xml` file makes this a
one-click affair — IntelliJ picks up `.run/` configurations automatically:

1. `docker compose up` so the dev server is running.
2. In IntelliJ, select the **Debug Angular (localhost:4200)** run
   configuration and click the **Debug** (bug) icon.
3. IntelliJ opens Chrome connected to the debugger. Set breakpoints
   directly in your `.ts` files — source maps served by the dev server
   map them back to your TypeScript.

If you prefer to create the configuration manually: **Run → Edit
Configurations → + → JavaScript Debug**, set URL to
`http://localhost:4200`, save, debug.

### Debugging SSR / server-side code (optional)

If your app uses Angular SSR and you need to debug the server bundle,
expose the Node inspector instead: add `"9229:9229"` to `ports` in
`docker-compose.yml`, run the SSR dev process with
`node --inspect=0.0.0.0:9229 ...`, and use IntelliJ's **Attach to
Node.js/Chrome** run configuration on `localhost:9229`.

## Common tasks

```bash
# Run tests inside the container
docker compose exec angular npx ng test

# Generate a component
docker compose exec angular npx ng generate component my-component

# Open a shell in the container
docker compose exec angular sh

# Stop everything
docker compose down
```

## Notes

- The image uses `node:22-alpine`; Angular 21 requires Node `^20.19`,
  `^22.12` or `>=24`, so Node 22 LTS is a safe default.
- Port 4200 carries both HTTP and the Vite HMR websocket, so no extra
  ports are needed for live reload.
