// Minimal Connect-JSON caller for test setup. Authenticates with the
// X-Dev-User-Id header (DEV_BYPASS_AUTH), so it skips the cookie/CSRF path —
// use it to arrange data, not to exercise the portal.

import type { SeedRole } from "./seed";
import { seedUsers } from "./seed";

export async function rpc<T = Record<string, unknown>>(
  baseURL: string,
  as: SeedRole,
  method: string,
  body: Record<string, unknown> = {},
): Promise<T> {
  const resp = await fetch(`${baseURL}/numun.v1.${method}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Connect-Protocol-Version": "1",
      "X-Dev-User-Id": seedUsers[as].id,
    },
    body: JSON.stringify(body),
  });
  const text = await resp.text();
  if (!resp.ok) {
    throw new Error(`${method} as ${as}: HTTP ${resp.status} ${text}`);
  }
  return JSON.parse(text) as T;
}
