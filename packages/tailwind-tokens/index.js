// Northwestern brand tokens for Tailwind.
//
// Placeholder values — final tokens (exact purple ramp, approved typefaces,
// type scale, spacing) land in M5 per IMPLEMENTATION_PLAN.md ambiguity #1.
// Source of truth: https://www.northwestern.edu/brand/visual-identity/

export const tokens = {
  colors: {
    "nu-purple": {
      DEFAULT: "#4E2A84", // Northwestern Purple (placeholder primary)
      50: "#F4F0FA",
      100: "#E4D9F0",
      200: "#C7B3E1",
      300: "#A98CD2",
      400: "#8B66C3",
      500: "#6E40B4",
      600: "#4E2A84",
      700: "#3D2167",
      800: "#2D194A",
      900: "#1E102E",
    },
  },
  fontFamily: {
    // Approved Northwestern faces are Akkurat (sans) and Whitney (display).
    // Until those are licensed, fall back to system sans + serif.
    sans: ["Inter", "ui-sans-serif", "system-ui", "sans-serif"],
    display: ["Inter", "ui-sans-serif", "system-ui", "sans-serif"],
  },
};

/** @type {import('tailwindcss').Config['theme']} */
export const theme = {
  extend: {
    colors: tokens.colors,
    fontFamily: tokens.fontFamily,
  },
};
