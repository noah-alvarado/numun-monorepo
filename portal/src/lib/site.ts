// Links into the public landing site (site/), which publishes each CMS
// background guide as a committee page.

const siteBaseUrl = (
  import.meta.env.VITE_SITE_BASE_URL ?? "http://localhost:4321"
).replace(/\/$/, "");

// guideUrl maps a committee's backgroundGuideRef
// (/content/background-guides/<conference>/<committee>.md) to its page on the
// site (/committees/<conference>/<committee>).
export function guideUrl(ref: string): string | null {
  const m = /^\/content\/background-guides\/([^/]+\/[^/]+)\.md$/.exec(ref);
  return m ? `${siteBaseUrl}/committees/${m[1]}` : null;
}
