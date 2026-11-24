// Accent color → static Tailwind class maps.
//
// The CMS `accentColor` field (content.config.ts) is a fixed enum of
// Northwestern secondary palette names. We map each to *complete* class
// strings here so Tailwind v4's content scanner can see them — never build
// class names dynamically from the value (e.g. `bg-nu-${accent}`), or the
// utility gets tree-shaken out of the bundle.
//
// This replaces the old Angular site's node-vibrant per-image color
// extraction with a brand-approved, editor-chosen palette.

export type Accent =
  | "purple"
  | "green"
  | "teal"
  | "blue"
  | "yellow"
  | "gold"
  | "orange";

/** Solid fill for a card's accent strip (bright variant). */
export const accentStrip: Record<Accent, string> = {
  purple: "bg-nu-purple",
  green: "bg-nu-green",
  teal: "bg-nu-teal",
  blue: "bg-nu-blue",
  yellow: "bg-nu-yellow",
  gold: "bg-nu-gold",
  orange: "bg-nu-orange",
};

/** Border color, used on hover to echo the accent. */
export const accentBorder: Record<Accent, string> = {
  purple: "hover:border-nu-purple-400",
  green: "hover:border-nu-green",
  teal: "hover:border-nu-teal",
  blue: "hover:border-nu-blue",
  yellow: "hover:border-nu-yellow-dark",
  gold: "hover:border-nu-gold-dark",
  orange: "hover:border-nu-orange",
};

/** Soft tinted background for a card header band. */
export const accentSoftBg: Record<Accent, string> = {
  purple: "bg-nu-purple-50",
  green: "bg-nu-green/10",
  teal: "bg-nu-teal/10",
  blue: "bg-nu-blue/10",
  yellow: "bg-nu-yellow/10",
  gold: "bg-nu-gold/10",
  orange: "bg-nu-orange/10",
};

/** Resolve an unknown/undefined accent to a safe default. */
export function asAccent(value: string | undefined | null): Accent {
  const allowed: Accent[] = [
    "purple",
    "green",
    "teal",
    "blue",
    "yellow",
    "gold",
    "orange",
  ];
  return allowed.includes(value as Accent) ? (value as Accent) : "purple";
}
