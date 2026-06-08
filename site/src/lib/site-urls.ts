// Env-aware URL helpers. `Astro.site` (from astro.config.mjs / SITE_BASE) is
// the apex origin (e.g. `https://test.numun.org`); subdomain hosts derive from
// its hostname so each env stays hermetic.
//
// Use these instead of hardcoding `portal.numun.org` etc. in pages so the
// test deploy doesn't silently link to prod.

export function portalUrl(site: URL, path = "/"): string {
  return new URL(path, `https://portal.${site.hostname}`).toString();
}

export function apiUrl(site: URL, path = "/"): string {
  return new URL(path, `https://api.${site.hostname}`).toString();
}

export function cmsUrl(site: URL, path = "/"): string {
  return new URL(path, `https://cms.${site.hostname}`).toString();
}

export function assetsUrl(site: URL, path = "/"): string {
  return new URL(path, `https://assets.${site.hostname}`).toString();
}
