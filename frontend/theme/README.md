# Theme

The app's look comes from the [Nuxt UI theme editor](https://ui.nuxt.com/). The two exported files live here unchanged:

| File            | Export                                                              |
| --------------- | ------------------------------------------------------------------- |
| `main.css`      | The exported `main.css` (colors, font, radius, light/dark tokens).  |
| `app.config.ts` | The exported `app.config.ts` (color aliases and component options). |

To change the theme, paste a new export over these files. Do not edit them by hand, and keep app-specific CSS in the custom block of `src/style.css`.

How they are wired:

- `src/style.css` imports `main.css`.
- `vite.config.ts` passes `app.config.ts`'s `ui` object to the Nuxt UI Vite plugin. `define-app-config.ts` provides the Nuxt `defineAppConfig` global so the export works unchanged. Restart the dev server after you change `app.config.ts`.
- Prettier ignores both files, so a paste does not fail `format:check`.

The editor loads its font from the web. The app is offline, so the font is bundled with [Fontsource](https://fontsource.org/) and imported in `src/style.css`. The `@fontsource/<font>` package registers the same family name as the export (`'Inter'`). If the new export uses another font, install `@fontsource/<font>` and import its 400, 500, 600 and 700 weights instead.
