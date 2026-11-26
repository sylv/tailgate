# tailgate's design

How the UI looks and why. Read this before changing anything visual. It's lyra's design system, cut down to what tailgate uses

The feel: calm, slim and flat, like Claude's web UI. Dense enough to show everything an admin needs on one screen, without feeling crowded. Nothing that reads as a generic component kit

## Rules

- Dark only for now. Every colour comes from the tokens in `src/globals.css`, never a raw grey or hex
- Slim and wide. Controls are 32px tall (`h-8`), small ones 28. Corners 6 to 8px, never pills. Body text 14px
- Borders, not fills. A control is a 1px `line` border. A fill means selected or under the pointer
- One plane. The page is `surface` grey edge to edge. No cards inside cards, no inset panels. Lists are one bordered box divided by 1px lines
- States are instant. Hover and press change a colour at once and nothing moves. Animation is only for what appears: menus, dialogs, an editor opening, a key reveal
- One accent, for meaning. Brand purple is the primary button (one per view), a ticked checkbox and focus rings. Nothing else is purple
- Status colours only say something. `danger` is broken or blocked, `warning` is risky or waiting, `success` is online
- Underline means "this opens something". Links, rows that open a page and nav tabs underline under the pointer. Buttons that do something don't
- Hover underlines, current is filled. In the nav tabs the current tab has a `raised` background
- Quiet facts. Details are muted text joined with commas, never dots, pills or badges
- Balance the page. Put facts on the right of a row (state, last used, match counts) and actions level with the heading, so a page isn't all on the left
- Say only what's needed. The admin set tailgate up, so a page doesn't explain what it's for. A muted line under a heading only when it says something the heading can't
- Warnings and errors are always an icon and text, under the thing they're about (`components/note.tsx`)
- Admin pages poll every 2 seconds while the tab is visible (`useLoad` with `POLL`), so there are no refresh buttons
- Plain words, no trailing periods, no title case

## Tokens

| Token | For |
|---|---|
| `page` | A step below `surface`, behind an open rule editor |
| `black` | Only the rules' save bar, which floats over everything |
| `surface` | The page |
| `raised` | Menus, dialogs, a row under the pointer, the current tab |
| `hover` | An item under the pointer inside a menu, the picked segment |
| `line`, `line-strong` | Borders, and a field's border under the pointer |
| `fg`, `fg-muted`, `fg-faint` | Text, secondary text, hints and placeholders |
| `brand`, `brand-bright` | The primary button and checkbox, focus rings |
| `danger`, `warning`, `success` | Status |
| `code-*` | The expression editor's strings, numbers, keywords and known values |

Figtree is the font, bundled from `@fontsource-variable/figtree` so nothing loads from a third party. Icons are Phosphor, regular weight, imported straight from `@phosphor-icons/react`

## Components

`src/ui/` holds components that know nothing about tailgate, mostly copied from lyra:

- `Button`: `primary`, `secondary` (bordered), `ghost`, `danger` (red text, bordered) and `link` (text that underlines)
- `IconButton`: icon only, always labelled with a tooltip
- `Input`, `Textarea`, `Field` (a label over a field) and `Labelled` (a label over something that isn't a field, since a `<label>` around a copy button copies when its text is clicked)
- `Checkbox`, `Segmented`, `Select`, `Menu`, `Tooltip`, `CopyField`, `ExternalLink`, `NavTab`, `Spinner`, `LogoMark`
- `Dialog`: lyra's. A panel in the middle over a black scrim, and on a phone a sheet from the bottom that can be dragged down to close. Its header and footer never scroll away

The CSP is `style-src 'self'`, which blocks the `<style>` tag radix's scroll lock injects. So menus are never modal, `Select` is built on the menu instead of radix's select, and `Dialog` is lyra's dialog built on a native `<dialog>` instead of radix's

## Pages

- Login, setup and the dashboard are one narrow column a little above centre. Login pages name the app being signed in to ("Sign in to photos") and never mention tailgate, since the people using them don't know what it is. Setup and "admins only" show the wordmark
- The admin pages have the wordmark on the left, the tabs in the middle and who's signed in on the right, over a page 1024px wide at most. Each page starts with its title and its main action level with it
- Users is a list of rows. A row opens the user's own page: profile and services on the left, keys and tailnet logins on the right. Save shows only once something changed
- Services is a table: the name (opening the app in a new tab), the container and the address tailgate proxies to, where it's reachable, whether login is on, and a status ring. The ring is 1px, green and empty when online, grey with a pause when the container is stopped, and shows a ! in yellow or red when something needs doing. Unused nodes are rows too, in purple. What needs doing shows under the row, like a signing command. The Services tab shows a yellow ! while anything does. On a phone the middle columns collapse into a line under the name
- Rules is the path a request takes, drawn as a line hanging into the left gutter so the text lines up with the page: four stages as big headings, each with a chevron on the line for the way traffic goes (a request comes in, sign in, signed in, on to the service), and only "Sign in" has a line under it, saying its rules are fixed, with the rules between them. A rule's dot is green when on, a hollow green ring when it only logs and grey when off. A rule reads as its name, condition and effects, with its mode on the right when it isn't on. Clicking it opens the editor in place, whose last button saves when the rule changed and closes when it didn't. Unsaved changes show a black bar at the bottom, as wide as the page's content
- OIDC is the issuer URL and a list of clients. Clicking a client opens its form in place. A new secret shows once, in a box above the list
- Secrets (keys, client secrets) show once in a `Dialog`, except on setup where the key is the page. Destructive actions ask first in a `Dialog`
