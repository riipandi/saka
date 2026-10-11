# UI Components Index: `packages/uilibs/src/components`

This document provides a comprehensive inventory of all available UI components in [`packages/uilibs/src/components`](file:///Users/ariss/Developer/github.com/riipandi/saka/packages/uilibs/src/components), their underlying primitives (Base UI, Radix, TanStack Table, StyleX), current adoption across [`packages/webapp`](file:///Users/ariss/Developer/github.com/riipandi/saka/packages/webapp), and architectural recommendations for UI/UX refinement.

---

## 1. Base Components (`uilibs/components/base/*`)

| Component | Import Path | Underlying Primitives & Description | Current Webapp Adoption | UX Refinement Recommendations |
| :--- | :--- | :--- | :--- | :--- |
| **Accordion** | `uilibs/components/base/accordion` | Collapsible accordion sections (Base UI) | Not used (0) | Ideal for nested policy rules, permission breakdown trees, and expandable FAQ/help cards. |
| **Alert Dialog** | `uilibs/components/base/alert-dialog` | High-friction confirmation modal dialog | Extensively used (~18 sites) | Maintain for destructive and high-risk actions (Delete User, Ban, Revoke Key, Terminate Sessions). |
| **Autocomplete** | `uilibs/components/base/autocomplete` | Text input with suggestion dropdown | Not used (0) | Implement for fast account searching, audit event filters, and dynamic role/group pickers. |
| **Avatar** | `uilibs/components/base/avatar` | Image avatar with initial fallback | Used via `user-avatar.tsx` | Standardize sizes across table rows, list items, and top navigation header. |
| **Button** | `uilibs/components/base/button` | Primary action button with variant system | Extensively used (~48 sites) | Standardize sizes (`sm`, `md`, `icon`), ghost variants, and loading states via `Spinner`/`LoaderText`. |
| **Checkbox** | `uilibs/components/base/checkbox` | Accessible form checkbox (Base UI) | Moderately used (~9 sites) | Replace raw `<input type='checkbox'>` in all dialogs and apply to multi-row selection tables. |
| **Collapsible** | `uilibs/components/base/collapsible` | Simple show/hide disclosure panel | Not used (0) | Use for collapsible advanced filter panels and raw webhook payload viewers. |
| **Combobox** | `uilibs/components/base/combobox` | Searchable multi-item select dropdown | Not used (0) | High priority for selecting user groups, client scopes, and notification audience filters. |
| **Context Menu** | `uilibs/components/base/context-menu` | Right-click contextual actions | Not used (0) | Optional desktop accelerator for row actions (Copy ID, Edit, Inspect). |
| **Dialog** | `uilibs/components/base/dialog` | General modal dialog overlay (Base UI) | Extensively used (~11 sites) | Standardize form modals (Create User, Issue Token, Test Webhook). |
| **Drawer** | `uilibs/components/base/drawer` | Slide-over inspector panel (Base UI) | Used in OIDC preview drawer | High priority for side-by-side inspection (Webhook delivery attempts, Audit log trace detail). |
| **Dropdown Menu** | `uilibs/components/base/dropdown-menu` | Accessible floating action menu | **Not used (0)** | **Critical priority**: Replace crowded inline action buttons on list rows with an accessible ellipsis (`...`) menu. |
| **Field** | `uilibs/components/base/field` | Accessible form field container with label & error | Extensively used (~26 sites) | Ensure all form inputs have associated `FieldLabel` and accessible error messages. |
| **Form** | `uilibs/components/base/form` | Form container wrapper | Not used directly | Form state integration. |
| **Input** | `uilibs/components/base/input` | Standard text, email, and password input | Extensively used (~29 sites) | Standardize input heights, borders, and disabled styling. |
| **Menubar** | `uilibs/components/base/menubar` | Desktop-grade menubar | Not used (0) | Optional for desktop-like toolbars. |
| **Meter** | `uilibs/components/base/meter` | Bounded progress meter | Not used (0) | Useful for quota display (e.g. rate limit usage, token expiry proximity). |
| **Navigation Menu** | `uilibs/components/base/navigation-menu` | Hierarchical site navigation header | Not used (0) | Suitable for self-service portal navigation header. |
| **Number Field** | `uilibs/components/base/number-field` | Stepper numerical input | Not used (0) | Replace raw number inputs in `/admin/settings` (session durations, lockout thresholds). |
| **OTP Field** | `uilibs/components/base/otp-field` | Segmented PIN / TOTP input slots | Used in MFA and Email verification | Maintain existing rock-solid implementation for verification codes. |
| **Popover** | `uilibs/components/base/popover` | Floating contextual container | Not used directly | Ideal for notification bell preview flyout and compact filter menus. |
| **Preview Card** | `uilibs/components/base/preview-card` | Hovercard preview popover | Not used (0) | Highly recommended for hover previews of User profiles and OIDC client metadata. |
| **Progress** | `uilibs/components/base/progress` | Deterministic progress bar | Not used (0) | Use during multi-step provisioning or bulk operations. |
| **Provider** | `uilibs/components/base/provider` | UI root theme and StyleX provider | Mounted at root (`__root.tsx`) | Global application theme configuration. |
| **Radio** | `uilibs/components/base/radio` | Mutually exclusive radio group | Not used (0) | Use for distinct policy selections (e.g. Access Mode: Open vs Invite-only). |
| **Scroll Area** | `uilibs/components/base/scroll-area` | Custom styled scroll container | Not used (0) | Essential for payload inspector boxes and long dialog body contents. |
| **Select** | `uilibs/components/base/select` | Accessible styled select dropdown | Moderately used in filter bars | Standardize sort order and page limit pickers. |
| **Separator** | `uilibs/components/base/separator` | Hairline divider element | Rarely used (manual borders) | Standardize section division across form cards and settings blocks. |
| **Slider** | `uilibs/components/base/slider` | Range slider control | Not used (0) | Applicable for percentage-based configurations (e.g. telemetry sampling ratio). |
| **Switch** | `uilibs/components/base/switch` | Toggle switch | Moderately used (~5 sites) | Maintain as standard for boolean toggles in platform settings. |
| **Tabs** | `uilibs/components/base/tabs` | Tab switcher (Base UI) | Moderately used (~5 sites) | Standardize category segmentation in settings and notification inbox. |
| **Toast** | `uilibs/components/base/toast` | Notification toast system | Mounted at root | Standardize user mutation feedback (success, failure, copy token). |
| **Toggle** | `uilibs/components/base/toggle` | Single pressed/unpressed button | Not used (0) | Suitable for list view switchers (grid vs list). |
| **Toggle Group** | `uilibs/components/base/toggle-group` | Connected toggle button group | Not used (0) | Excellent for quick status filters (All / Active / Revoked). |
| **Toolbar** | `uilibs/components/base/toolbar` | Standard action toolbar wrapper | Not used (0) | Standardize layout of search bars, sort selects, and CTA buttons above tables. |
| **Tooltip** | `uilibs/components/base/tooltip` | Accessible hover/focus tooltip | **Not used (0)** | **Critical priority**: Attach tooltips to icon-only buttons, truncated TypeIDs, relative timestamps, and badges. |

---

## 2. Extra Components (`uilibs/components/extra/*`)

| Component | Import Path | Purpose & Capabilities | Current Webapp Adoption | UX Refinement Recommendations |
| :--- | :--- | :--- | :--- | :--- |
| **Alert** | `uilibs/components/extra/alert` | Contextual callout banner | Moderately used (~8 sites) | Use for system announcements, step-up notices, and security advisories. |
| **Aspect Ratio** | `uilibs/components/extra/aspect-ratio` | Fixed-ratio container | Not used (0) | Ensure consistent aspect ratios for user avatars and client logo previews. |
| **Badge** | `uilibs/components/extra/badge` | Compact colored status indicator | Extensively used (~21 sites) | Standardize semantic variants (`success`, `destructive`, `secondary`, `outline`). |
| **Breadcrumb** | `uilibs/components/extra/breadcrumb` | Hierarchical route navigation | **Not used (0)** | **Critical priority**: Implement breadcrumbs on all admin detail pages (`/admin/users/$id`, `/admin/webhooks/$id`, `/admin/oidc-clients/$id`). |
| **Button Group** | `uilibs/components/extra/button-group` | Segmented connected button container | Rarely used (~1 site) | Group pagination buttons and action triggers seamlessly. |
| **Calendar** | `uilibs/components/extra/calendar` | Date picker (react-day-picker) | Not used (0) | Integrate for audit log date-range filtering. |
| **Card** | `uilibs/components/extra/card` | Structured container (`Header`, `Content`, `Footer`) | Extensively used (~30 sites) | Foundational block for settings sections and overview panels. |
| **Command** | `uilibs/components/extra/command` | Command palette system (cmdk) | **Not used (0)** | **High-impact UX feature**: Mount global `⌘K` command palette for instant navigation across admin and account screens. |
| **Data Grid** | `uilibs/components/extra/data-grid` | Virtualized TanStack v9 table with column pinning | Not used (0) | Available for massive datasets requiring column reordering or inline cell editing. |
| **Description List** | `uilibs/components/extra/description-list` | Semantic key-value list (`<dl>`, `<dt>`, `<dd>`) | **Not used (0)** | **Critical priority**: Replace custom flex/div layouts on all resource detail screens with `DescriptionList`. |
| **Empty** | `uilibs/components/extra/empty` | Empty state layout (`Title`, `Description`, `Actions`) | Extensively used (~23 sites) | Standardize empty states with an actionable primary CTA button. |
| **File Upload** | `uilibs/components/extra/file-upload` | Drag-and-drop file uploader | Not used (0) | Use for custom avatar uploads and certificate imports. |
| **Icon** | `uilibs/components/extra/icon` | Standardized icon wrapper | Not used directly | Wrap third-party icons to enforce strict sizing tokens. |
| **Input Group** | `uilibs/components/extra/input-group` | Input with addons (icons, buttons) | **Not used (0)** | **Critical priority**: Upgrade bare search inputs with leading search icons, trailing clear buttons, and inline copy buttons for secrets. |
| **Input Password** | `uilibs/components/extra/input-password` | Password input with toggle visibility | Moderately used (~5 sites) | Standard for all credential input forms. |
| **Input Phone** | `uilibs/components/extra/input-phone` | International phone input with country picker | Not used (0) | Ready for SMS MFA enrollment when phone identity is configured. |
| **Item** | `uilibs/components/extra/item` | Row item layout (`Media`, `Content`, `Actions`) | Overused across all lists | Restrict to chronological feeds and mobile-friendly vertical stacks; avoid using for tabular multi-attribute records. |
| **Kanban** | `uilibs/components/extra/kanban` | Drag-and-drop board | Not used (0) | Out of scope for core authentication surfaces. |
| **Kbd** | `uilibs/components/extra/kbd` | Keyboard shortcut badge (`⌘K`, `Esc`, `Enter`) | **Not used (0)** | Add to search fields, dialog close buttons, and command palette triggers. |
| **Label** | `uilibs/components/extra/label` | Standalone accessible label | Not used (uses `FieldLabel`) | Use when a label is needed outside a form `Field` wrapper. |
| **Loader Text** | `uilibs/components/extra/loader-text` | Animated skeleton text placeholder | Moderately used (~8 sites) | Maintain for text-level loading states. |
| **Pagination** | `uilibs/components/extra/pagination` | Semantic pagination (`nav`, `ul`, `li`, links) | **Not used (0)** | **Critical priority**: Replace all hand-rolled `turnPage` previous/next buttons with `Pagination`. |
| **Resizable** | `uilibs/components/extra/resizable` | Split panel container | Not used (0) | Excellent for split-screen webhook payload inspection. |
| **Sheet** | `uilibs/components/extra/sheet` | Slide-out overlay drawer | Not used (0) | Alternative to full-page navigation for secondary forms. |
| **Skeleton** | `uilibs/components/extra/skeleton` | Pulse loading skeleton block | Not used (0) | Prevent layout shifts while loading tables and cards. |
| **Sortable** | `uilibs/components/extra/sortable` | Drag-and-drop reorderable list | Not used (0) | Apply for ordering OIDC claims or identity provider precedence. |
| **Spinner** | `uilibs/components/extra/spinner` | SVG loading indicator | Extensively used (~45 sites) | Standard spinner indicator. |
| **Table** | `uilibs/components/extra/table` | Semantic HTML table (`Header`, `Body`, `Row`, `Cell`) | **Not used (0)** | **Critical priority**: Replace vertical `Item` cards in Audit Logs, API Keys, Users, Blocklists, and Deliveries with `Table`. |
| **Text** | `uilibs/components/extra/text` | Typed typography system (`h1`-`h4`, `body-1`/`2`) | Extensively used (~50 sites) | Foundational typographic building block. |
| **Textarea** | `uilibs/components/extra/textarea` | Multiline textarea input | Moderately used (~7 sites) | Used for descriptions, redirect URI lists, and raw payloads. |

---

## 3. Charts and Brand Icons

- **TanStack Charts (`uilibs/components/charts/*`)**:
  - `AreaChart`, `BarChart`, `DonutChart`, `LineChart`, `Heatmap`, `ProgressCircle`, `Tracker`.
  - *Recommendation*: Introduce compact telemetry widgets in `/overview` and `/admin/audit` (e.g. sign-in success vs. failure ratios, active sessions over time).
- **Brand Icons (`uilibs/components/icons/*`)**:
  - `GitHubIcon`, `GoogleIcon`, `ViteIcon`, `TelegramIcon`, `FacebookIcon`, `InstagramIcon`, `LinkedInIcon`, `WhatsAppIcon`, `XIcon`.
  - *Recommendation*: Continue using on OAuth SSO sign-in buttons.
