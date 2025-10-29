# Recomma Agent Handbook

## Mission Snapshot
- Web UI that replays 3Commas trading activity on Hyperliquid.
- React 19 + TypeScript + Vite 6, styled with Tailwind 4 and shadcn/ui.
- Talks to the Recomma Ops API; OpenAPI types live in `schema.d.ts` and feed `src/types/api.ts`.

## Getting Set Up
- Use Node 18+ and npm (see `package.json`).
- Install deps with `npm install`.
- Build assets with `npm run build`; the Go backend embeds the `dist` output, this is done during CI only.
- Do **not** rely on `npm run dev`—WebAuthn passkeys require the SPA to share the exact origin/port with the Go binary.
- During iterative work, use `npm run build -- --watch` (or rerun `npm run build` manually); backend reloads are handled outside the agent workflow.

### Git Submodules
- Clone with submodules so the 3Commas spec is available: `git clone --recurse-submodules git@github.com:recomma/recomma.git`
- For existing clones, pull the spec by running: `git submodule update --init --recursive`


## Core Entry Points
- `src/main.tsx` bootstraps React and injects `App`.
- `src/App.tsx` orchestrates runtime state:
  - Polls `/vault/status` to decide between setup, login, or the main dashboard.
  - Renders `SetupWizard` for first-time configuration and `Login` when the vault is sealed.
  - Shows `StatsCards` + `OrdersTable` once authenticated.

## App States & Flows
- **Setup**: `src/components/setupwizard` handles three phases—3Commas API credentials, Hyperliquid secrets, and WebAuthn passkey confirmation. It posts to `/vault/setup`.
- **Login**: `src/components/Login.tsx` exchanges WebAuthn assertions for a session cookie via `/webauthn/login/*`.
- **Dashboard**:
  - `src/components/StatsCards.tsx` aggregates `/api/orders` and listens on `/sse/orders`.
  - `src/components/OrdersTable/OrdersTable.tsx` provides grouped deal views, detail dialogs, bulk actions, and dynamic column visibility.
  - **LyteNyte Grid**: All tabular interactions run through `@1771technologies/lytenyte-core` (`Grid`, data source hooks, column definitions). Reuse its helpers (`useClientRowDataSource`, column renderers) and keep configuration in sync with upstream docs when adding features.

## Data & API Contracts
- Regenerate shared types with `npx openapi-typescript ../../openapi.yaml -o schema.d.ts` whenever the backend OpenAPI spec changes; commit the updated `schema.d.ts` together with related client updates.
- Always construct URLs with `buildOpsApiUrl` from `src/config/opsApi.ts` so runtime config, env vars, and fallbacks apply.
- Types come from `src/types/api.ts`; prefer these aliases over inline typing.
- Real-time updates rely on SSE endpoints (`/sse/orders`). `useOrdersData` handles wiring, fallback mock data, and deal refreshes—reuse it where possible.
- When hitting new endpoints, extend `schema.d.ts` via the backend OpenAPI source rather than editing it by hand.

## Component & Module Overview
- `src/components/OrdersTable` bundles table layouts, filters (`filters/`), renderers, and mock data.
- `src/components/BotsDealsExplorer.tsx` + dialogs provide cards and side panels when drilling into bots/deals.
- Shared shadcn components live in `src/components/ui`.
- `src/hooks` contains reusable hooks (`useHyperliquidPrices`, etc.).
- `src/utils/logger.ts` exposes a runtime-aware logger gated by `window.__RECOMMA_CONFIG__.DEBUG_LOGS`.

## Styling & UI Conventions
- Tailwind 4 utility classes drive layout; `cn` in `src/lib/utils.ts` merges class lists.
- Favor existing shadcn/ui primitives and Radix-based components for dialogs, popovers, etc.
- Keep layouts responsive—App shell uses `container` widths and flex parents; match these patterns.
- SVG favicon and other assets live under `public/` and `favicon/`; regenerate favicons via `npm run favicon`.

## Working Agreements for Agents
1. **Respect the runtime contract**: only fetch through `buildOpsApiUrl` and preserve credentials (`credentials: 'include'`).
2. **Guard asynchronous work**: mirror `isMountedRef` patterns when adding hooks to avoid state updates after unmount.
3. **Type safely**: lean on the OpenAPI-derived types; extend them in `schema.d.ts` rather than creating duplicates.
4. **UI consistency**: reuse existing dialogs, cards, filter controls, and status color helpers (`statusToneClasses`, etc.).
5. **Error UX**: provide user-friendly toasts via `sonner` and keep fallback mock data for offline flows consistent.

## Validation Checklist
- `npx eslint` for linting
- `npx tsc --noEmit` to catch TypeScript
- Do not execute the Go binary from this workspace; coordinate with the backend runtime to validate WebAuthn and SSE flows.
- Manual QA for multi-step flows (setup, login, dashboard interactions) remains a downstream responsibility—surface any assumptions clearly.

## Reference Material
- Runtime config contract injected via `window.__RECOMMA_CONFIG__` by the Go backend; document changes there if you add flags.
