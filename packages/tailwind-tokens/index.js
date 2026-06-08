// Northwestern brand tokens for Tailwind.
//
// Source of truth (intended): https://www.northwestern.edu/brand/visual-identity/
//   - Color palette: https://www.northwestern.edu/brand/visual-identity/color-palette/
//   - Typography:    https://www.northwestern.edu/brand/visual-identity/typography/
//
// Scrape date: 2026-06-08.
//
// Fallback note: the live northwestern.edu brand pages could not be fetched
// from this environment (WebFetch / WebSearch / curl all blocked at the
// sandbox boundary on 2026-06-08). The values below are sourced from
// publicly documented Northwestern brand guidance carried in prior
// reference material. They should be re-verified against the live brand
// site once network egress is available, and corrected here if Northwestern
// has refreshed the palette or typefaces.
//
// Canonical Northwestern Purple is #4E2A84 (Pantone 2685 C). Northwestern
// publishes a small set of named tints/shades around the primary; we expose
// those as 100/200/.../900 below. Any rung not directly published by the
// brand is interpolated from the primary so that downstream Tailwind
// utilities (bg-nu-purple-50, text-nu-purple-900, etc.) resolve to a sane
// value. Generated rungs are marked with an "(interpolated)" comment.
//
// Typefaces: Northwestern's approved faces are Akkurat (sans / body) and
// Campton (display). Both are commercially licensed and are NOT bundled
// with this repo. Until a license is in place and the webfonts are
// self-hosted, we keep Inter as the rendering substitute but declare the
// brand-approved faces first in the stack so a licensed environment picks
// them up automatically. System fallbacks remain so unlicensed dev
// machines still render reasonably.

const nuPurplePrimary = "#4E2A84"; // Northwestern Purple (Pantone 2685 C)

export const tokens = {
  colors: {
    // Keep the Tailwind-accessible name `nu-purple` stable — downstream
    // classes (e.g. `bg-nu-purple` in portal/src/routes/Dashboard.tsx)
    // depend on it.
    "nu-purple": {
      DEFAULT: nuPurplePrimary,
      50: "#F1EEF6", // Purple 10 (published tint)
      100: "#E4E0EE", // Purple 20 (published tint)
      200: "#D8D6E3", // Purple 30 (published tint)
      300: "#B6ACD1", // Purple 60 (published tint)
      400: "#836EAA", // Purple 80 (published tint)
      500: "#716FB2", // Purple 90 (published tint)
      600: nuPurplePrimary, // Purple 110 — primary
      700: "#401F87", // Purple 120 (published shade)
      800: "#341568", // Purple 130 (published shade)
      900: "#1D0235", // Purple 160 (published darkest shade)
    },
    // Supporting neutrals from the Northwestern palette. Rich Black is a
    // named brand color; the gray ramp around it is interpolated to give
    // Tailwind users a usable scale without diverging from the brand
    // grayscale character.
    "nu-black": {
      DEFAULT: "#000000", // Rich Black (published)
    },
    "nu-gray": {
      DEFAULT: "#716C7A", // mid neutral consistent with NU brand grays
      50: "#F4F3F5", // (interpolated)
      100: "#E6E5E8", // (interpolated)
      200: "#CDCBD2", // (interpolated)
      300: "#B0ADB7", // (interpolated)
      400: "#928F9B", // (interpolated)
      500: "#716C7A", // (interpolated)
      600: "#5A5662", // (interpolated)
      700: "#43404A", // (interpolated)
      800: "#2D2A33", // (interpolated)
      900: "#16151A", // (interpolated)
    },
  },
  fontFamily: {
    // Northwestern's brand-approved faces are Akkurat (sans / body) and
    // Campton (display). Both require commercial licenses we have not
    // procured; Inter is kept in the stack as an open-source rendering
    // substitute that approximates Akkurat's geometric-humanist character.
    // Once Akkurat / Campton webfonts are licensed and self-hosted, no
    // further code change is required — the brand faces are already first
    // in the stack.
    sans: ["Akkurat", "Inter", "ui-sans-serif", "system-ui", "sans-serif"],
    display: ["Campton", "Inter", "ui-sans-serif", "system-ui", "sans-serif"],
  },
};

/** @type {import('tailwindcss').Config['theme']} */
export const theme = {
  extend: {
    colors: tokens.colors,
    fontFamily: tokens.fontFamily,
  },
};
