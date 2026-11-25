// Env-aware URL helpers for the sibling hosts (portal, API, CMS, assets).
// They hang off the environment's apex (e.g. `test.numun.org`), which is
// SITE_APEX at build time and defaults to SITE_BASE's host. They don't use
// `Astro.site`: a PR preview lives at `pr-<n>.preview.test.numun.org` but must
// link to the test portal, exactly like test does.
//
// Use these instead of hardcoding `portal.numun.org` etc. in pages so the
// test deploy doesn't silently link to prod.

declare const __SITE_APEX__: string;

export function portalUrl(path = "/"): string {
  return new URL(path, `https://portal.${__SITE_APEX__}`).toString();
}

export function apiUrl(path = "/"): string {
  return new URL(path, `https://api.${__SITE_APEX__}`).toString();
}

export function cmsUrl(path = "/"): string {
  return new URL(path, `https://cms.${__SITE_APEX__}`).toString();
}

export function assetsUrl(path = "/"): string {
  return new URL(path, `https://assets.${__SITE_APEX__}`).toString();
}
