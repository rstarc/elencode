# Classic fantail logo kit

A pixel-aligned vector redraw of the selected classic fantail concept. The silhouette has been regularized onto a square grid; this is a cleaned redraw, not a byte-exact trace of the generated image.

## Which file to use

| Use | File |
| --- | --- |
| Editable/scalable master | `svg/fantail.svg` |
| Light backgrounds | `svg/fantail-black.svg` |
| Dark backgrounds | `svg/fantail-white.svg` |
| Small icons | `svg/fantail-micro.svg` |
| Browser tab | `web/favicon.svg` and `web/favicon.ico` |
| Apple touch icon | `web/apple-touch-icon.png` |
| Avatar / services that need PNG | `png/fantail-avatar-512.png` |
| Transparent PNG | `png/fantail-black-512.png` or white equivalent |
| Compact terminal logo | `terminal/fantail-small.txt` (16 columns × 8 lines) |
| Full terminal logo | `terminal/fantail.txt` (33 columns × 17 lines) |
| Non-Unicode terminal | `terminal/fantail-ascii.txt` (32 columns × 16 lines) |
| Braille terminal logo (elencode's startup screen) | `terminal/fantail-braille.txt` (10 columns × 5 lines) |

The main SVG has a 40 × 40 viewBox, including clear space. Use 40, 80, 120, 160 px, etc. for equal-sized pixel steps. Other dimensions still work, but snapping to screen pixels may make some steps a pixel wider than others. At 16–32 px, use the separately simplified micro mark. Do not stretch width and height independently.

## Website

Copy `svg/` and `web/` to your website's public assets directory. Example:

```html
<img src="/assets/svg/fantail-black.svg"
     width="80" height="80" alt="Fantail">

<link rel="icon" href="/assets/web/favicon.ico" sizes="any">
<link rel="icon" href="/assets/web/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="/assets/web/apple-touch-icon.png">
```

Use `alt=""` if the image is decorative or the adjacent label already supplies the same name.

For an interface with a custom color, paste the contents of `svg/fantail.svg` inline into your HTML/component. It uses `fill="currentColor"`, so the inline SVG follows CSS `color`. Add an accessible name (`role="img" aria-label="Fantail"`) or `aria-hidden="true"` if decorative. A standalone SVG loaded through `<img>` does not inherit the surrounding page's `color`; use the black/white exports in that case.

```css
.brand-icon {
  width: 80px;
  height: 80px;
  color: #16816a;
}
```

Add `class="brand-icon"` to the inline SVG. The adaptive SVG favicon selects black or white using the system color preference. The ICO has a light background to remain visible in either theme.

## GitHub repository README

Commit this kit under `assets/logo/`, then use:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo/svg/fantail-white.svg">
  <source media="(prefers-color-scheme: light)" srcset="assets/logo/svg/fantail-black.svg">
  <img alt="Fantail" src="assets/logo/svg/fantail-black.svg" width="120" height="120">
</picture>
```

Paths above assume `README.md` is in the repository root. Use `png/fantail-avatar-512.png` for a profile or organization avatar where a PNG upload is needed. A repository README logo and an account/organization avatar are separate placements.

## Terminal

Most terminals display characters rather than SVGs. These UTF-8 text exports use `▀`, `▄`, and `█`: each character represents two vertically stacked pixels. They use the terminal's existing foreground color and contain no escape sequences.

```sh
cat assets/logo/terminal/fantail-small.txt
cat assets/logo/terminal/fantail.txt
```

Use a monospace font with Unicode block characters. Font metrics can change the apparent aspect ratio; the ASCII export is a portable fallback. For a larger logo, use the full export instead of enlarging character spacing.

Example Python CLI integration, with no third-party runtime dependencies:

```python
from pathlib import Path
import sys

# Adjust this path to where your application bundles its assets.
logo = Path(__file__).resolve().parent / "assets/logo/terminal/fantail-small.txt"
if sys.stdout.isatty():
    print(logo.read_text(encoding="utf-8"), end="")
```

Keep banners out of JSON, pipes and machine-readable command output. Include the asset files in your package configuration when distributing the CLI.

## Editing and regenerating

`build.py` contains the master pixel grid and the separately drawn micro grid: `#` is foreground, `.` is transparent. Edit those grids and regenerate to keep all variants consistent:

```sh
python -m pip install pillow
python build.py
```

The script builds SVG paths, transparent PNGs, opaque avatar/touch icons, favicons, terminal text and a preview sheet. SVG output has no embedded bitmap, fonts, scripts or external resources. PNGs use nearest-neighbor scaling to preserve hard pixel edges. SVGs remain editable in a vector editor; direct SVG edits will be overwritten if you run the generator again.

Colors: near-black `#111111`, white `#ffffff`; avatar/icon background `#faf9f6`. Recolor the master or generator, then regenerate rather than recoloring each PNG independently.
