// Northwestern University brand tokens for Tailwind.
//
// Sources verified 2026-06-08:
//   - https://www.northwestern.edu/brand/visual-identity/
//   - https://www.northwestern.edu/brand/visual-identity/color-palettes/
//   - https://www.northwestern.edu/brand/visual-identity/color-palettes/secondary-palette/
//   - https://www.northwestern.edu/brand/visual-identity/fonts-typography/
//
// Cross-reference: numun.org self-hosts Periódico Text, Akkurat Pro, and
// Campton OTF files. The site's applied text colors match Angular Material
// defaults (rgba(0,0,0,0.87) ≈ Rich Black 80%, rgba(0,0,0,0.54) ≈ Rich
// Black 50%) which line up with Northwestern's published Rich Black tints,
// so the ramp doubles as a faithful "imitate the live site" palette.

const nuPurplePrimary = "#4E2A84"; // Northwestern Purple (Pantone 2685 C)

export const tokens = {
  colors: {
    // ── Primary: Northwestern Purple ────────────────────────────────────
    // `nu-purple` is the stable Tailwind name — downstream classes in
    // /portal and /site depend on it. Every rung is a Northwestern-
    // published value; nothing here is interpolated.
    "nu-purple": {
      DEFAULT: nuPurplePrimary,
      50: "#E4E0EE", // Purple 10
      100: "#CCC4DF", // Purple 20  (RGB 204, 196, 223)
      200: "#B6ACD1", // Purple 30
      300: "#A495C3", // Purple 40  (RGB 164, 149, 195)
      400: "#836EAA", // Purple 60
      500: "#684C96", // Purple 80  (RGB 104, 76, 150)
      600: nuPurplePrimary, // Northwestern Purple — primary
      700: "#401F68", // Purple 120
      800: "#30104E", // Purple 140 (RGB 48, 16, 78)
      900: "#1D0235", // Purple 160 (RGB 29, 2, 53)
    },

    // ── Neutrals: Rich Black tints ──────────────────────────────────────
    // Northwestern publishes Rich Black + 10/20/50/80% tints. We expose
    // those at their natural rungs and fill the gaps via linear
    // interpolation between adjacent published values so a full Tailwind
    // 50–900 scale works for downstream callers. Interpolated rungs are
    // marked.
    "nu-black": {
      DEFAULT: "#000000", // Rich Black (Pantone Rich Black)
      50: "#EFECEC", //  (interpolated — between white and Rich Black 10%)
      100: "#D8D6D6", // Rich Black 10%
      200: "#BBB8B8", // Rich Black 20%
      300: "#9C9898", //  (interpolated — between 20% and 50%)
      400: "#878282", //  (interpolated — between 20% and 50%)
      500: "#716C6B", // Rich Black 50%
      600: "#58524F", //  (interpolated — between 50% and 80%)
      700: "#46403E", //  (interpolated — between 50% and 80%)
      800: "#342F2E", // Rich Black 80%
      900: "#000000", // Rich Black
    },

    // ── Secondary palette ───────────────────────────────────────────────
    // Six hue families, each with a `DEFAULT` (bright) and `dark`. These
    // are the 12 PMS values Northwestern publishes on the secondary
    // palette page; pairings match the brand's bright/dark grouping.
    //
    // Brand guidance: "use rarely and sparingly… for items that require
    // differentiation, for example, within charts and graphs, or for
    // updates or callout buttons in digital applications."
    "nu-green": {
      DEFAULT: "#58B947", // PMS 360  — bright
      dark: "#008656", //   PMS 7725 — dark
    },
    "nu-teal": {
      DEFAULT: "#7FCECD", // PMS 318  — bright
      dark: "#007FA4", //   PMS 314  — dark
    },
    "nu-blue": {
      DEFAULT: "#5091CD", // PMS 279  — bright
      dark: "#0D2D6C", //   PMS 294  — dark (navy)
    },
    "nu-yellow": {
      DEFAULT: "#EDE93B", // PMS 394  — bright
      dark: "#D9C826", //   PMS 611  — dark (mustard)
    },
    "nu-gold": {
      DEFAULT: "#FFC520", // PMS 7548 — bright
      dark: "#CA7C1B", //   PMS 7571 — dark (rust)
    },
    "nu-orange": {
      DEFAULT: "#EF553F", // PMS 7625 — bright
      dark: "#D85820", //   PMS 1595 — dark
    },
  },

  fontFamily: {
    // Stacks below use the Google Fonts Northwestern names on its
    // typography page as the free web counterparts to the commercial
    // brand faces:
    //   sans (body)         → IBM Plex Sans  ("substitute for Akkurat Pro")
    //   display (headlines) → Poppins        ("headlines and subheads on the web")
    //   serif (body serif)  → Noto Serif     ("body text on the web")
    // Commercial faces (Akkurat Pro / Campton / Periódico Text) are
    // intentionally NOT first in the stack: they're never resolved unless
    // self-hosted, so leading with them just added noise. When/if we
    // license and self-host any of them, add it as the first entry here
    // and load the @font-face in the layout.
    sans: ["IBM Plex Sans", "ui-sans-serif", "system-ui", "sans-serif"],
    display: ["Poppins", "ui-sans-serif", "system-ui", "sans-serif"],
    serif: ["Noto Serif", "ui-serif", "Georgia", "Cambria", "serif"],
  },
};

/** @type {import('tailwindcss').Config['theme']} */
export const theme = {
  extend: {
    colors: tokens.colors,
    fontFamily: tokens.fontFamily,
  },
};
