# Design

loopgate's one web page, the live tracking page of a Job, uses GitHub Primer's dark tokens: neutral, plain, and read at a glance, like the terminal it sits beside. It has one theme, dark, and loads no web font and no external asset.

## Typography
- Sans: `-apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans", Helvetica, Arial, sans-serif`
- Mono: `ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace`; the event log, ids, paths and commands are mono
- Sizes: 12px (labels, meta, log), 14px (body), 16px (section titles, figures), 24px (headline); weights 400 and 600
- Line height: 1.5 for sans, 24px for the 12px log

## Color

| Token | Value | Use |
|---|---|---|
| `--bg` | `#0d1117` | page |
| `--subtle` | `#151b23` | card, table header |
| `--inset` | `#010409` | event log, gate output |
| `--border` | `#3d444d` | panel edges |
| `--divider` | `#262c36` | rows inside a panel |
| `--fg` | `#f0f6fc` | text, Claude's messages |
| `--muted` | `#9198a1` | labels, tool input, times |
| `--accent` | `#4493f8` | running state, the newest event, links |
| `--success` | `#3fb950` | done, pass |
| `--attention` | `#d29922` | Blocked, manual |
| `--danger` | `#f85149` | failed, exhausted, fail |

A state color's chip and icon halo use it at 15%; the status card is tinted with it at 7% over `--subtle` and has a 2px top edge in it. The state colors match `loopctl list`, except that running uses `--accent` in place of the terminal's cyan.

## Spacing and shape
- Spacing scale: 4, 8, 12, 16, 24, 32px; 32px between page blocks, 24px inside the card, 12 to 16px in rows
- Radius: 6px; status dots, stepper nodes and the state icon are circles
- Content width: at most 960px, 16px side gutter
- Icons: Lucide (check, x, pause, chevron-right, external-link), inline SVG
- No motion but the chevron's turn, no gradients, no glow
