# Hub Console — Build Blueprint

Derived from the claude-design prototype `console/design-prototype.dc.html` (2504
lines, `<x-dc>` single-file prototype) + the generation brief
`design/009-management-console/claude-design-prompt.md`. This is the reference
for the real **React + Vite + TypeScript + Tailwind + shadcn/ui** implementation
in `console/`. Backend contract (GraphQL `/hugr`, REST `/api/v1/*`, `/skills/*`,
chat frame protocol, auth) is in the design-prompt; this file captures the
**visual + interaction spec** of the prototype.

> The prototype is all inline `style=""` on CSS variables (no classes) with a
> custom `<x-dc>` framework and fully mocked in-browser logic (mutations emit
> toasts echoing the real API call). Port the *look, layout, states, and
> behavior*; wire to the real APIs from the design-prompt.

---

## 1. Design tokens → Tailwind theme

Light (`:root`) / Dark (`[data-theme="dark"]`). Drive dark via a `data-theme`
attribute (or Tailwind `darkMode: ['selector','[data-theme="dark"]']`).

| token | light | dark | use |
|---|---|---|---|
| `--bg` | `#f6f8f7` | `#0e1212` | app canvas |
| `--surface` | `#ffffff` | `#151b1a` | cards/panels/bars |
| `--surface2` | `#eff3f2` | `#1b2321` | nested fills, segmented track, chips |
| `--surface3` | `#e6ecea` | `#232d2b` | deeper fills, count pills, progress track |
| `--border` | `#e2e8e6` | `#242e2c` | hairline |
| `--border2` | `#d3dcda` | `#313d3a` | stronger (inputs/dashed) |
| `--text` | `#16211f` | `#e7edeb` | primary |
| `--text2` | `#5a6a67` | `#9caba7` | secondary |
| `--text3` | `#8b9a96` | `#647370` | muted / eyebrows |
| `--accent` | `#1C7D78` | `#3db3aa` | brand (teal) |
| `--accent-hi` | `#166661` | `#54c4bb` | hover |
| `--accent-soft` | `#e3efee` | `#173330` | active nav bg, chips, banners |
| `--accent-text` | `#ffffff` | `#08201d` | on-accent text |
| `--green` | `#1d9d64` | `#3cc182` | ready/running/allow |
| `--amber` | `#c5820c` | `#dfa631` | loading/starting/waiting/filtered |
| `--red` | `#cf4438` | `#e26a5e` | error/deny/destructive |
| `--blue` | `#2f6fae` | `#6aa5d8` | artifact/incoming-relation/module |
| `--green-soft` | `#e2f4ea` | `#12322a` | badge bg |
| `--amber-soft` | `#f8efdb` | `#332a12` | badge bg |
| `--red-soft` | `#fae7e5` | `#3a1d1a` | badge bg |
| `--shadow` | `0 1px 2px rgba(20,30,28,.06),0 8px 24px rgba(20,30,28,.08)` | `0 1px 2px rgba(0,0,0,.4),0 8px 24px rgba(0,0,0,.35)` | cards/menus |
| `--shadow-lg` | `0 4px 12px rgba(20,30,28,.10),0 24px 64px rgba(20,30,28,.18)` | `0 4px 12px rgba(0,0,0,.5),0 24px 64px rgba(0,0,0,.6)` | modals/drawers |

**Typography:** IBM Plex Sans (400/500/600/700) body; **IBM Plex Mono** (400/500/600)
for ALL identifiers — names, IDs, DSNs, GraphQL types, hashes, token counts,
budgets, timestamps. Base `13.5px / 1.45`, antialiased. Scale (px): 9–16, +18/24
for stat numbers/titles. Eyebrows: `10.5px`, 600/700, `letter-spacing .05–.08em`,
uppercase, `--text3`. Headings/stat numbers `letter-spacing -.01/-.02em`.
**Radii:** 4–5 chips, 6–9 buttons/inputs/nav, 10–11 cards/panels, 12–14
composer/modals/bubbles, 99 pills/dots/avatars. Bubbles asymmetric: user
`13px 13px 3px 13px`, agent `13px 13px 13px 3px`.
**Spacing:** pages `padding:20px 22px; gap:14–18`; cards `14–16`; table rows
`8–10px 16px`. **Fixed sizes:** topbar `50`, sidebar `216`, chat rail `248`,
live panel `280`, artifacts panel `264`, roles rail `250`, agent drawer `520`,
ds drawer `460`, key/schema/filter drawers `420–440`.
**Animations:** `pulse` (opacity 1↔.35, loading/waiting), `spin` (tree loaders),
`fadeUp` (translateY 6px + fade — modals/menus/messages), `blinkc` (streaming caret).
Input focus: `outline:2px solid var(--accent)`. Modal overlay `rgba(10,16,15,.45)`
+ `backdrop-filter:blur(2px)`; drawer overlay `rgba(10,16,15,.32)`.

---

## 2. App chrome

- **Two top-level modes:** `theme` (light/dark) + `persona` (admin/owner) with a
  **"view as" segmented switcher** in the sidebar (Admin/Personal) — role-gates
  the nav. Prototype default lands on `light`+`admin`.
- **Sidebar (216px, `--surface`):** brand (logo 28px + "hugr hub" / "console") →
  nav groups → persona switcher at bottom ("view as · role-gated nav").
  - **Admin nav:** Monitoring · Chat `[count]` · Agents `[count]` · **Marketplace:** Skills · **Platform:** Data Sources, Catalogs, Schema Explorer, Roles & Permissions, API Keys · Me / Access.
  - **Owner nav:** Chat `[count]` · Agents `[count]` · **Marketplace:** Skills · Me / Access. (no Monitoring, no Platform.)
  - Active item: `accent-soft` bg + `accent` text + 600. Each item: 15px stroke SVG icon + label + optional count pill.
- **Topbar (50px):** screen title (left) → spacer → connection pill (green dot
  "hub · connected") → notifications bell (red count badge; dropdown 330px:
  "Notifications" / "Mark all read" / rows `dot + text + "{agent} · {time}"` /
  footer "Scheduled tasks & agent session events") → theme toggle (sun/moon) →
  user menu (avatar initials + name + "· role"; dropdown 230px: name/email/role,
  "My access & identity", "Sign out" red).
- **Content:** `<main>` flex column; each screen a route. Standard page:
  `flex:1; overflow-y:auto; padding:20px 22px; gap:14–18`. Chat/Roles/Schema use
  a two-column (own left rail + detail) layout instead.
- Icons: 12–16px, `viewBox 0 0 16 16`, `fill:none; stroke:currentColor;
  stroke-width 1.4–1.8; linecap round`.

---

## 3. Screens (routes)

**Login** (`signedOut`): centered 360px card — logo 52px, "Hugr Hub Console",
subtitle, "Continue with Keycloak" primary, footnote "OIDC Authorization Code +
PKCE · issuer from /console/config.json".

**Monitoring** (admin only): 4 stat tiles (Agents running `4/5`, Active sessions
`4`, Tokens·24h `1.28M`, Data sources ready `4/6`) → `1.4fr/1fr`: **Data source
health** (dot|name|type|path|STATUS + "Open data sources →") + **Fleet**
(dot|name|"N sess"|runtime); **LLM budgets** (name|"used/limit"|progress;
`hub.db.llm_budgets`) → full-width **Recent activity** (mono time | tag
`AGENT`/`HITL`/`ARTIFACT`/`PLATFORM`/`FLEET` | text).

**Chat** — see §4.

**Agents:** header (subtitle by persona + admin "＋ Create agent"). Table cols:
*(dot)* | **Agent** (name + mono id) | **Role** (mono) | **Owner** | **Sessions** |
**Desired / Runtime** (chip + colored) | **Actions** (admin "▶ Start" green /
"⏸ Stop" amber). Row → **agent drawer** (520px, tabs Overview / Config override
(JSON) / Access grants). Admin "＋ Create agent" → **4-step wizard**: name → data
role (pick/create) → config_override JSON → bootstrap secret (copy-once).
Footnote "hub.my_agent_instances · start_agent / stop_agent re-checked per call".

**Skills:** segmented **Catalog** / **Capability grants** (grants admin-only) +
"↑ Publish bundle…". *Catalog:* search + count + card grid (mono name + "v{ver}" +
"by {publisher}" + desc + capability chips + sha256 + "↓ Bundle"; "Load more").
*Grants:* editable **matrix** rows=roles × cols=capabilities(+Publish); cells
toggle ✓/· ; "＋ Role" / "＋ Capability" adders. Footnotes cite
`GET /skills/catalog`, `grant_skill_capability`/`revoke_skill_capability`/`set_skill_publish`.

**Data Sources** (Platform): header + "＋ Add data source". Table cols: *(dot)* |
**Name** (mono link + desc) | **Type** | **Prefix** | **Path / DSN** | **Flags**
(module/read-only chips) | **Actions** (Load/Unload, Reindex, CP, Schema).
Add/edit → **ds drawer** (460px, incl. nested catalogs). "Schema" → describe
drawer. Footnote `core.data_sources · data_source_status(name)`.

**Catalogs** (Platform): card grid — mono name + type chip + desc + mono path +
"Linked sources" chips (each ✕ unlink + dashed "＋ link"). `core.catalog_sources`
/ `core.catalogs`.

**Roles & Permissions** (Platform): 2-col. Left rail (250px) `core.roles` — name
+ "N rules"/"no rules" + "off" badge(disabled) + desc. Right: header + "＋ Add
rule"; **accent info banner** "Access is ALLOW by default. A rule only takes
effect when it hides, disables, or row-filters a (type, field)."; red error
banner on validation; **Rules table** cols **Type** | **Field** | **Hidden** |
**Deny** | **Row filter / defaults** | *(del)* (inputs + toggles + filter-builder
drawer); empty state; **Effective access** card (tabs Data schema / Agent &
console; `check_access`) — schema tab = role-scoped GraphQL tree with verdict
badges allow/hidden/deny/filtered; hub tab = ✓/✕ capability rows. Footnote
`insert/update/delete_role_permissions · [$auth.user_id] [$auth.role]`.
**Live validation:** rejects `neq`, requires `_not` in filter JSON.

**API Keys** (Platform): "＋ Create key" → modal; reveal-once green banner
("copy it now, it won't be shown again" + Copy + ✕). Table cols **Name** |
**Role** | **Expiry** | **Status** (active/temporal/disabled badge) |
**Description** | **Actions** (Enable/Disable + Del). `core.api_keys`.

**Schema Explorer** (Platform — *extra, beyond the design-prompt IA; keep under
Platform*): 2-col. Left = **Tree**/**Model** toggle; search + kind select
(All/Tables/Views/Functions); recursive unified-GraphQL tree (Query/Mutation →
modules → generated ops → args/fields/relations) with lazy-expand spinners &
kind chips. Right = node detail: badges + **Description** textarea + "Save
description"; **Fields** table (**#**|**Name**|**Type**|**Description**, click to
edit); **Relations** (out/in badges). Editing feeds LLM schema summaries
(`_schema_update_*_desc`, `core.meta`).

**Me / Access:** identity card (avatar + name + "email · user_id" + role badge);
**Capabilities** (✓/✕ + mono cap + note: `hub:management.admin`, `hugr:query`,
`artifact:write`, `net:fetch`, `skills:publish`); **My agent grants** (dot +
agent + owner/member badge; `hub.db.user_agents · owner ⊃ member`). Footnote
`function.core.auth.me + my_permissions · admin = hub:management.admin`.

---

## 4. Chat UI (the deepest surface)

3-region row: **rail 248px** | **conversation (centered, 860px normal / 360px
"panel" preview mode)** | optional **right panel** (Live view 280px XOR Artifacts
264px; both hidden in narrow mode).

- **Rail:** "＋ New chat" (accent) → toggles inline **agent picker** popover
  ("Pick an agent" + rows dot+name+access) → `create_chat(agent_id)`. Chat list
  **grouped by project** (folder icon + uppercase project name); each chat = name
  (ellipsized, active accent/600) + "{agent} · {last}". Active = accent-soft bg.
- **Conversation header:** chat name + agent; status pill (dot + label by session
  state); toggles "⇥ Panel 360px / ⇤ Full width" (JupyterLab embed preview),
  "Live view", "Artifacts · {n}" (active → accent).
- **Message list** (`aria-live=polite`), frame kinds:
  - **User bubble** — right, accent bg, `accent-text`, radius `13 13 3 13`, 78% max, pre-wrap.
  - **Agent bubble** — left, surface+border, radius `13 13 13 3`, 86% max; **blinking caret** while streaming; optional usage footer mono "final · ↑ N · ↓ N tok".
  - **Reasoning block** — collapsible; "Thinking…" (pulsing) while streaming → "Thought for {elapsed}"; open = left-border italic muted text.
  - **Tool call/result block** — collapsible pill: chevron + wrench + mono tool name + state ("running…" amber / "done" green); open = mono args (`surface2`) + result (left-border-3 green "→ …").
  - **Artifact chip** — accent-soft icon tile + mono filename + "artifact_produced · {size}"; opens artifacts panel.
  - **System line** — centered pill ("Turn cancelled …", "Approval rejected …").
- **Composer:** optional running banner (pulsing "Agent is working — {detail}" /
  "waiting for your approval" + "■ Cancel turn" red) + auto-grow textarea
  (placeholder "Message the agent… (Enter to send)", 22–120px) + round send
  button (accent when draft non-empty & not running). Enter (no shift) sends.
- **Live view panel:** Context budget (mono "84.1k / 200k" + progress) · Missions
  & subagents (indented tree: dot + name + colored state, incl. "(async)" blue) ·
  Scheduled tasks (name + "{when}").
- **Artifacts panel:** header + "Upload"; cards mono filename + "{size} · by
  {by} · {time}" + "↓ Get".
- **HITL inquiry modal** (440px, `role=dialog`): amber icon + "Approval required"
  / "The agent paused and needs your decision"; question (bold) + boxed context
  (surface2); "Optional reason (sent on reject)"; buttons **Reject** (red) /
  **Approve + auto-approve tool** (neutral, flex 1.4) / **Approve** (accent).
  Shape `{request_id, type:'approval'|'clarification', question, context}` — build
  approval now; keep clarification path for the real protocol.
- **Turn simulation to reproduce as real SSE handling:** user msg → reasoning
  streams → tool pending→done → agent streams → `wait_approval` opens inquiry →
  approve pushes tool + result + artifact + final streamed msg w/ usage → idle;
  reject/cancel push system frames. Session states `idle`/`active`/`wait_approval`
  → label+color. **This maps 1:1 to the real frame kinds** (`agent_message`
  consolidated deltas→final, `reasoning`, `tool_call`/`tool_result`,
  `inquiry_request`, `session_status`, `extension_frame` artifact) — see
  design-prompt §"Chat frame protocol".

---

## 5. Reusable component catalog (→ shadcn set)

Primary/secondary-ghost/icon(30px)/danger/status(green,amber) **buttons**;
**segmented control** (track surface2, active seg surface+shadow); **underline
tabs**; **status dot** (7–9px, pulse when loading); **status/verdict badge**
(*-soft bg + matching text, mono/uppercase); **count pill** (surface3/text2);
**card** (surface+border, r11); **table** (CSS-grid header eyebrow row + grid
rows, hairline separators); **input/textarea/select** (border2, r7–9, focus
accent outline; mono variant); **search field** (icon + borderless input in
bordered pill); **checkbox/radio** (accent-color); **progress bar** (5px track,
colored fill); **modal** (centered overlay+blur, r14, shadow-lg, fadeUp);
**drawer** (right slide, overlay, left border, shadow-lg, backdrop-close);
**dropdown/popover** (surface+border+shadow-lg, r10, fadeUp); **toast** (bottom-
right stack, `border-left:3px`, auto-dismiss 3.6s, echoes the API call);
**avatar** (accent circle + initials); native `title` **tooltips**; **empty
state** (dashed box, muted); **info/alert banner** (*-soft bg+border: accent
info / red error / green reveal); **collapsible disclosure** (rotating ▸ chevron).

---

## 6. Behavior / state to preserve

- **Route-gating by persona:** owner reaches only `chat/agents(read-only)/skills(catalog)/me`; admin gets everything. Enforce in nav + per-control (`isAdmin`/`canStart`/`canStop`). In the REAL app persona = the user's actual role/capabilities (`hub:management.admin`, `user_agents` grants), not a toggle — but keep a dev "view as" if cheap.
- **Overlays** are mutually-managed: user-menu vs notifs; live-view XOR artifacts; drawers/modals close on backdrop.
- **Tabs:** agent (overview/config/access), skills (catalog/grants), effective-access (schema/hub), schema (tree/model).
- **Async/streaming:** timers for streaming + tool latency + load/start transitions (start_agent→~1.8s→running; load_data_source→~1.6s→ready; tree expand→~650ms spinner). Real app: SSE frames + GraphQL mutations + optimistic UI.
- **Clipboard copy-once** for bootstrap secret / API key / new key.
- Mono API-hint footnotes under each screen (keep as subtle captions — they document the backing call).

---

## 7. Coverage vs design-prompt IA

All intended screens present: Data Sources ✅, Catalogs ✅, Roles & Permissions ✅
(rich), API Keys ✅, Agents ✅ (+drawer +wizard), Skills ✅ (catalog+grants+publish),
Monitoring ✅, Chat ✅ (deepest), Me/Access ✅. **Extras:** **Schema Explorer**
(unified GraphQL browser + description editor — substantial; place under
Platform), Login, Notifications dropdown, persona switcher. **Thin/absent:** no
Projects management screen (projects are only chat-grouping metadata — add simple
CRUD later), budgets read-only on Dashboard only, no settings screen.

---

## Build order (proposed)

1. **Scaffold** Vite+React+TS+Tailwind+shadcn in `console/` (base `/console/`,
   outDir `dist`); Tailwind theme from §1 tokens (CSS vars + `data-theme` dark);
   IBM Plex fonts (self-host/inline — CSP: no external CDN in prod).
2. **Shell**: sidebar + topbar + routing + theme + persona/role gating + the
   reusable component primitives (§5) as shadcn components.
3. **Auth**: OIDC PKCE + `/console/config.json`; identity probe
   (`me`/`my_permissions`) → role gating.
4. **Data layer**: typed `/hugr` GraphQL client + `/api/v1` REST + SSE reader
   (`fetch`+`ReadableStream`); TanStack Query.
5. **Screens** by domain (start Platform + Agents + Skills + Monitoring + Me —
   mostly GraphQL CRUD over `core.*`/`hub.*`), then **Schema Explorer**.
6. **Chat microfrontend** — separable `mountChat(el, props)` / `<hub-chat>`
   package on the real frame protocol; embed in the SPA + a Lumino wrapper.
