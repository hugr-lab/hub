# Hub Console — Porting Guide (for screen implementers)

You are porting one domain of the Hugr Hub Console from the design prototype to
the real React app. The scaffold, UI kit, data layer, auth, and shell already
exist and BUILD GREEN. Do **not** touch them — only add your screen(s) and
`src/api/<domain>.ts`. Match the prototype's look; wire real backend ops.

## Ground rules

- Stack: React 18 + TS (strict, **noUnusedLocals + noUnusedParameters** — no dead
  vars/imports) + Tailwind (tokens as semantic colors) + Radix-based kit.
- Import the kit from `@/components/ui` (barrel). Import page helpers from
  `@/components/shell/Page`. `@/` = `src/`.
- Icons: `PathIcon` (kit, 16-viewBox stroke paths) or `lucide-react` (bundled,
  CSP-safe). No external assets/CDN.
- Colors are CSS-variable-backed Tailwind classes — **use them, never hardcode
  hex**: `bg-bg bg-surface bg-surface2 bg-surface3 border-border border-border2
  text-text text-text2 text-text3 bg-accent text-accent bg-accent-soft
  text-accent-text text-green text-amber text-red text-blue bg-green-soft
  bg-amber-soft bg-red-soft`. Shadows: `shadow-card shadow-lg`. Radii: `rounded-btn
  rounded-card rounded-chip rounded-modal`. Font: default sans; `font-mono` for
  ALL identifiers (names, ids, DSNs, GraphQL types, hashes, tokens, timestamps).
  Eyebrow labels: `className="eyebrow"`.
- After writing, run `pnpm build` from `console/` and fix all TS/build errors in
  YOUR files. Leave the tree green.

## UI kit API (`@/components/ui`)

- `Button` — props `variant` (`primary|secondary|ghost|danger|danger-ghost|green|amber`,
  default secondary), `size` (`sm|md|lg|icon`), `asChild`. e.g.
  `<Button variant="primary" size="sm">＋ Add</Button>`.
- `Card`, `CardHeader`, `CardTitle`, `CardBody` — surface+border+r-card panel.
- `Eyebrow` — uppercase 10.5px label. `StatTile {label,value,sub?,subColor?}` — dashboard tile.
- `Badge {tone?:'neutral'|'green'|'amber'|'red'|'blue'|'accent', mono?}` — soft badge.
- `Dot {state, size?}` — status dot (pulses on loading/starting/waiting). `dotColor(state)` helper.
- `Pill` — count pill.
- `Input`, `Textarea`, `Select` (all accept `mono?`), `Label`, `Field {label,hint,children}`,
  `SearchField` (icon + input). Focus ring is automatic.
- `Segmented {options:[{value,label}], value, onChange, size?}` — segmented control.
- `Tabs {tabs:[{value,label}], value, onChange}` — underline tabs.
- `DataTable<Row> {columns, rows, getKey, onRowClick?, empty?}` where
  `Column<Row> = {key, header, width?/*grid track e.g. '1fr'|'120px'|'minmax(0,1.4fr)'*/, align?, cell:(row,i)=>node}`.
- `Modal {open,onOpenChange,title?,description?,footer?,width?,children}` — centered.
- `Drawer {open,onOpenChange,title?,subtitle?,footer?,width?,children}` — right slide.
- `Menu/MenuTrigger/MenuContent/MenuItem{danger?,onSelect}/MenuSeparator` — dropdown (Radix).
- `Popover/PopoverTrigger/PopoverContent` — popover (Radix). `PopoverAnchor` too.
- `Toggle {checked,onCheckedChange}` (switch), `CheckboxBox {checked,onCheckedChange}`.
- `Progress {value/*0..100*/, color?}`.
- `Avatar {initials,size?}`, `initialsOf(name)`.
- `EmptyState {title,description?,action?,icon?}`, `Banner {tone:'info'|'error'|'reveal'}`, `Spinner`.
- `useToast()` → `{toast, success, error}` — call after mutations to echo the API op.
- `Collapsible {header, defaultOpen?, open?, onOpenChange?, children}` — rotating chevron.
- `PathIcon {d, size?, strokeWidth?}`, `navIcons`, `themeIconPath`.

## Page layout (`@/components/shell/Page`)

- `<Page>` — standard scroll container (padding 20/22, column, gap-4). Wrap your screen in it
  (except 2-column screens like Roles/Schema which use their own `flex` row layout).
- `<PageHeader title subtitle? actions? />`.
- `<ApiHint>core.data_sources · data_source_status(name)</ApiHint>` — mono caption citing the backing call. Put one under each screen.

## Data layer

- GraphQL: `import { postGraphQL } from '@/lib/graphql'` →
  `await postGraphQL<Resp>(queryString, variables?)`. Throws `GraphQLRequestError`
  on `errors[]`. **Filters have NO `neq`; negate with `_not:{field:{eq:$x}}`.**
- REST: `import { restJSON, restRaw } from '@/lib/rest'` →
  `restJSON<T>(path, {json?, method?})`; `restRaw` for blobs. Base path is same-origin.
- Demo: `import { withDemo } from '@/lib/demo'`. Wrap every fetcher:
  `return withDemo(MOCK, async () => { const d = await postGraphQL(...); return shape(d) })`.
  This makes the screen fully interactive offline (`/console/?demo=1`). ALWAYS provide realistic MOCK seeds.
- Reads via TanStack Query: `useQuery({ queryKey:['dataSources'], queryFn: listDataSources })`.
  Mutations: `useMutation({ mutationFn, onSuccess: () => { qc.invalidateQueries({queryKey:['dataSources']}); toast.success('...') } })`.
  Get `qc` from `useQueryClient()`. **Use queryKey `['agents']` and `['chats']`** for those two
  lists (the sidebar reads their length for badges).

### api module pattern (`src/api/<domain>.ts`)

```ts
import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

export interface DataSource { name: string; type: string; /* … */ }

const MOCK: DataSource[] = [ /* realistic rows */ ]

export async function listDataSources(): Promise<DataSource[]> {
  return withDemo(MOCK, async () => {
    const d = await postGraphQL<{ core: { data_sources: DataSource[] } }>(
      `query { core { data_sources { name type prefix as_module path description disabled read_only } } }`,
    )
    return d.core.data_sources
  })
}
// mutations: postGraphQL(`mutation($data:...){ core { insert_data_sources(data:$data){ name } } }`, { data })
```

GraphQL shapes: reads `query { <ns> { <table>(filter/order_by/limit/offset) {…} } }`;
CRUD `mutation { <ns> { insert_<table>(data:{…}) / update_<table>(filter:,data:) / delete_<table>(filter:) {…} } }`;
functions `mutation { function { <ns> { <fn>(args){ success message } } } }`.
`core` = platform admin tables; `hub` = agents/chats. Exact tables/fields/functions are in
`design/009-management-console/claude-design-prompt.md` (§Backend contract) — follow it precisely.

## Prototype reference

`console/design-prototype.dc.html` (inline-style `<x-dc>` prototype). Read YOUR screen's
line range (given in your task) for exact layout/labels/columns/states, plus its render-model
in the `<script>` (search the screen label). `console/BUILD-BLUEPRINT.md` §3 summarizes each screen.
Reproduce structure, columns, empty/loading/error states, toasts, and the mono API footnotes.
Do NOT reproduce the `<x-dc>` framework or inline styles — use the kit + Tailwind classes.
