# Multi-stage Dockerfile for fdh-portal-web.
#
# We use npm (not pnpm) in Docker because pnpm's strict isolation clashes
# with Next.js 14's implicit styled-jsx runtime requirement and with
# `output: "standalone"`'s file tracer. Local developers continue to use
# pnpm for speed and disk efficiency.
#
# We invoke `npm install --legacy-peer-deps` rather than `npm ci`:
#   - npm ci needs a package-lock.json that exactly matches package.json,
#     but the host repo doesn't commit one (we use pnpm-lock.yaml).
#   - npm install resolves fresh per build; a few extra seconds in CI in
#     exchange for not maintaining two lockfiles.
#   - --legacy-peer-deps because next-auth@beta has a peer-dep mismatch
#     with knip that npm 11+ refuses to resolve by default.

FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json ./
RUN npm install --legacy-peer-deps --no-audit --no-fund

FROM node:22-alpine AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
RUN addgroup --system --gid 1001 nodejs && \
    adduser --system --uid 1001 nextjs

# Next.js standalone output bundles only the server + needed deps.
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static
COPY --from=builder --chown=nextjs:nodejs /app/public ./public

USER nextjs
EXPOSE 3000
ENV PORT=3000 HOSTNAME=0.0.0.0
CMD ["node", "server.js"]
