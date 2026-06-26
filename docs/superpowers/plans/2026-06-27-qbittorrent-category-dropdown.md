# Plan — qBittorrent category dropdown (issue #91)

Date: 2026-06-27
Issue: https://github.com/artyomsv/marauder/issues/91 (enhancement)
Branch: `91-qbittorrent-category-dropdown`

## Problem & goal

When manually adding/editing a topic, the **Category** field is free-text. It
drifts from the categories that actually exist in the download client. For
qBittorrent (which exposes its categories via the WebUI API), fetch the
existing categories and offer them as suggestions, while still allowing
free-text entry.

Category in Marauder is a **path segment**, not a client-native label only
(see `registry.EffectiveDownloadDir` / `SanitizeCategory`). The dropdown is a
*convenience for picking a value* — resolution semantics stay identical.

## Acceptance criteria

1. A new optional client capability `registry.WithCategories` exists; clients
   that can list categories implement it, others don't.
2. qBittorrent implements `WithCategories` by calling
   `GET /api/v2/torrents/categories` and returning a sorted name list.
3. New endpoint `GET /api/v1/clients/{id}/categories` (JWT, user-scoped)
   returns `{ "supported": bool, "categories": [string] }`.
4. The AddTopic / EditTopic form renders the Category field as a **combobox**
   (`<input list=…>` + `<datalist>`): suggestions come from the selected
   client's categories when supported & non-empty; otherwise plain free-text.
5. The effective client is the one selected in the form, or the user's
   **default** client when "Use default client" is chosen.
6. Non-qBittorrent clients (Transmission, Deluge, µTorrent, downloadfolder)
   fall back to free-text (`supported:false`).
7. Fail-open: client unreachable / not-yet-tested / zero categories ⇒ the
   field still works as free-text; no blocking error, no console spam.
8. Resolution semantics unchanged — verified by existing `category_test.go`.
9. Backend + frontend tests cover capability, plugin fetch, handler, and UI.
10. Docs (`docs/clients.md`) + marketing site (`site/src/data/clients.ts`)
    mention the category dropdown for qBittorrent.

## qBittorrent API verification

`GET /api/v2/torrents/categories` (WebUI API v2, qBit 4.1+) returns a JSON
object keyed by category name:

```json
{
  "TV":    {"name":"TV","savePath":"/downloads/tv"},
  "Movies":{"name":"Movies","savePath":""}
}
```

So listing is feasible. Nested categories ("TV/Anime") arrive as `/`-joined
names — valid path-segment values, kept verbatim. Will be confirmed live
against the `deploy/docker-compose.test-clients.yml` qBittorrent during
verification.

## Design decisions (alternatives considered)

- **Capability vs. always-on**: use an optional `WithCategories` capability so
  only qBittorrent (today) exposes a list; mirrors the existing `WithStatus`
  pattern exactly (compile-time `var _ registry.WithCategories = …`).
- **Dedicated endpoint vs. `/system/info` flag**: a per-client-instance
  endpoint returns *live* categories using the stored encrypted config
  (categories are instance data, not static plugin metadata). Chosen over
  baking a static `supports_categories` into `/system/info`.
- **Combobox (`<input>`+`<datalist>`) vs. hard `<select>`**: datalist keeps
  free-text (issue says "instead of *or alongside*"), so a user can still
  type a brand-new category (path segment) that doesn't exist in qBit yet, and
  an edit-mode value not in the list is never clobbered. Native HTML, zero new
  deps, degrades to a normal input when no suggestions.
- **Fail-open**: this is a convenience. Any fetch error ⇒ empty suggestions ⇒
  plain free-text. The handler logs a warn and returns `supported:true,
  categories:[]` rather than a 5xx.

## Backend changes

### 1. Capability interface — `registry/registry.go`

```go
// WithCategories is an optional client capability: enumerate the categories
// the client already knows about so the UI can offer them as suggestions when
// picking a topic's category. Category remains a path segment in Marauder
// (see EffectiveDownloadDir); this list is a convenience, not a constraint.
type WithCategories interface {
    Client
    Categories(ctx context.Context, rawConfig []byte) ([]string, error)
}
```

Place near `WithStatus`. Add registry-level test asserting qBittorrent
satisfies it and (e.g.) downloadfolder does not (type assertion).

### 2. qBittorrent — `clients/qbittorrent/qbittorrent.go`

- Add `var _ registry.WithCategories = (*plugin)(nil)`.
- Implement:

```go
func (p *plugin) Categories(ctx context.Context, rawConfig []byte) ([]string, error) {
    var cfg Config
    if err := json.Unmarshal(rawConfig, &cfg); err != nil {
        return nil, fmt.Errorf("bad config: %w", err)
    }
    s, err := p.session(ctx, cfg)
    if err != nil {
        return nil, err
    }
    endpoint := strings.TrimRight(cfg.URL, "/") + "/api/v2/torrents/categories"
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
    if err != nil {
        return nil, err
    }
    resp, err := s.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("qbit categories: %w", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        b, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("qbit categories %d: %s", resp.StatusCode, string(b))
    }
    var m map[string]struct{ Name string `json:"name"` }
    if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
        return nil, fmt.Errorf("decode qbit categories: %w", err)
    }
    names := make([]string, 0, len(m))
    for k := range m {
        names = append(names, k)
    }
    sort.Strings(names)
    return names, nil
}
```

(Adds `sort` import.) New `categories_test.go` with a fake mux (reuse the
pattern from `category_test.go`): returns a categories object, asserts sorted
names; plus an empty-object case and a non-200 error case.

### 3. Handler — `api/handlers/clients.go`

```go
// Categories handles GET /clients/{id}/categories. Returns the categories the
// client already knows about (qBittorrent), so the AddTopic form can offer
// them as suggestions. Fail-open: a client that can't list categories, or a
// transient fetch error, yields an empty list (the field stays free-text).
func (h *Clients) Categories(w http.ResponseWriter, r *http.Request) {
    uid, perr := currentUserID(r)
    if perr != nil { problem.Write(...); return }
    id, ierr := uuid.Parse(chi.URLParam(r, "id"))
    if ierr != nil { problem.Write(... bad id ...); return }
    c, err := h.Clients.GetByID(r.Context(), id, uid)
    if err != nil { // ErrNotFound -> 404, else 500
        ...
    }
    plugin := registry.GetClient(c.ClientName)
    cat, ok := plugin.(registry.WithCategories)
    if plugin == nil || !ok {
        writeJSON(w, 200, map[string]any{"supported": false, "categories": []string{}})
        return
    }
    raw, err := h.Master.Decrypt(c.ConfigEnc, c.ConfigNonce)
    if err != nil { problem.Write(... 500 ...); return }
    names, err := cat.Categories(r.Context(), raw)
    if err != nil {
        // fail-open: log warn, return supported+empty so UI degrades to free-text
        log.Warn()...
        writeJSON(w, 200, map[string]any{"supported": true, "categories": []string{}})
        return
    }
    if names == nil { names = []string{} }
    writeJSON(w, 200, map[string]any{"supported": true, "categories": names})
}
```

- Wire route in `api/router.go` next to the other client routes:
  `r.Get("/clients/{id}/categories", clientsH.Categories)`.
- Use the existing zerolog logger seam in handlers (check how other handlers
  log; if none, return empty silently — do not introduce a new global logger).
- New `clients_categories_test.go` (handler test): supported client returns
  sorted names; unknown/unsupported client returns `supported:false`;
  not-found id → 404; fetch error → `supported:true, categories:[]`.

## Frontend changes

### 4. `lib/api.ts`

- Extend the `ClientOption`/list view consumed by the form to include
  `is_default` (already on the backend `clientView`) and `client_name`.
- Add:

```ts
getClientCategories: (id: string) =>
  api.get<{ supported: boolean; categories: string[] }>(`/clients/${id}/categories`),
```

### 5. `lib/queryKeys.ts`

```ts
clientCategories: (id: string) => ["clientCategories", id] as const,
```

### 6. `components/topics/TopicForm.tsx`

- Extend the local `ClientOption` interface: `{ id; display_name; is_default }`.
- Compute the effective client:

```ts
const effectiveClientId =
  delivery.clientId || clients.find((c) => c.is_default)?.id || "";
```

- Categories query (enabled when an effective client exists):

```ts
const categoriesQuery = useQuery({
  queryKey: QK.clientCategories(effectiveClientId),
  queryFn: () => api.getClientCategories(effectiveClientId),
  enabled: !!effectiveClientId,
  staleTime: 60_000,
  retry: false,
});
const categorySuggestions =
  categoriesQuery.data?.supported ? categoriesQuery.data.categories : [];
```

- Category field becomes a combobox:

```tsx
<Input
  id="category"
  list={categorySuggestions.length ? "category-suggestions" : undefined}
  value={delivery.category}
  onChange={...}
  placeholder="tv"
/>
{categorySuggestions.length > 0 && (
  <datalist id="category-suggestions">
    {categorySuggestions.map((c) => <option key={c} value={c} />)}
  </datalist>
)}
```

- Helper text: when suggestions present, add "Pick an existing qBittorrent
  category or type a new one." (i18n via `useT` if the surrounding strings are
  already keyed — match the file's existing approach; current strings here are
  literal English, so keep literal to match).
- `useState` ceiling: no new `useState` is added (categories live in React
  Query), so the ≤8 rule is respected.

### 7. Tests

- `TopicForm` already has tests? Check `frontend/src/components/topics/` for an
  existing `TopicForm.test.tsx`/`AddTopicCard.test.tsx`. Add a test: mock
  `/clients/{id}/categories` → `{supported:true, categories:["TV","Movies"]}`,
  assert the `<datalist>` options render; and a `supported:false` case asserts
  no datalist. Use MSW/`vi.mock` consistent with the existing test setup.

## Docs & site

- `docs/clients.md` qBittorrent section: note the Category field now suggests
  existing qBittorrent categories (fetched live) while still accepting free
  text.
- `site/src/data/clients.ts` qBittorrent `description`: add "Category
  autocomplete from your qBittorrent categories." (keep it short).
- Check `site/src/pages/features.astro` / `integrations.astro` for a natural
  spot; add only if it fits without churn.
- Update root `CLAUDE.md` plugin section: mention the new `WithCategories`
  capability + the `/clients/{id}/categories` endpoint (structural change rule).

## Edge cases

| Case | Behaviour |
|---|---|
| Client not qBittorrent | `supported:false` → free-text, no datalist |
| qBittorrent unreachable / bad creds | fail-open empty → free-text |
| qBittorrent has zero categories | `supported:true, []` → free-text |
| "Use default client" chosen | resolve via `is_default`; if no default, no query |
| Edit topic, stored category not in list | datalist doesn't constrain input; value preserved |
| Nested category "TV/Anime" | kept verbatim; valid path segment |
| Category typed not in qBit | accepted; created as folder segment + qBit label on Add (#75) |
| Auth/ownership | endpoint user-scoped via `GetByID(id, uid)`; 404 on foreign id |

## Verification checklist

- `go build ./... && go vet ./... && go test -race ./...` (Docker golang:1.25).
- `npm run typecheck && npm test && npm run build` (Docker node:20-alpine).
- Live: bring up base+test-clients qBittorrent, create a couple of categories
  in its WebUI, create a qBit client in Marauder, hit
  `GET /clients/{id}/categories`, confirm names; open AddTopic and confirm the
  datalist suggestions.
- `/code-review` → fix all findings.
- PR green, docs + site updated, local Docker app rebuilt for manual check.

## Out of scope

- Categories for Transmission/Deluge/µTorrent (no qBit-equivalent concept;
  Deluge "labels" could be a follow-up).
- Creating new categories in qBittorrent from Marauder.
- Caching/invalidation beyond React Query's 60s staleTime.
