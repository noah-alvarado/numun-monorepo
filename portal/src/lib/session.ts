// In-memory current-user state for the SolidJS app shell. We never store
// auth tokens here — the session is a cookie the browser holds; this module
// only caches the User profile returned by UserService.GetMe so the app can
// reactively render.

import { createSignal, type Signal } from "solid-js";
import { userClient, authClient } from "./api";
import type { User } from "@/gen/numun/v1/users_pb";
import type { ExchangeRequest } from "@/gen/numun/v1/auth_pb";

const [currentUser, setCurrentUser] = createSignal<User | null>(null);
const [loading, setLoading] = createSignal(false);

export const userSignal: Signal<User | null> = [currentUser, setCurrentUser];
export const loadingSignal: Signal<boolean> = [loading, setLoading];

/**
 * loadCurrentUser hits UserService.GetMe and caches the result. Returns null
 * when the call returns Unauthenticated — the caller should redirect to /sign-in.
 * Any other error throws so the app shell can surface it.
 */
export async function loadCurrentUser(): Promise<User | null> {
  setLoading(true);
  try {
    const resp = await userClient.getMe({});
    setCurrentUser(resp.user ?? null);
    return resp.user ?? null;
  } catch (err) {
    const e = err as { code?: string };
    if (e?.code === "unauthenticated") {
      setCurrentUser(null);
      return null;
    }
    throw err;
  } finally {
    setLoading(false);
  }
}

/**
 * exchange POSTs Cognito tokens to AuthService.Exchange to mint the
 * server-side session cookie. On success the portal calls loadCurrentUser
 * to populate state.
 */
export async function exchange(
  req: Partial<ExchangeRequest>,
): Promise<User | null> {
  await authClient.exchange({
    idToken: req.idToken ?? "",
    accessToken: req.accessToken ?? "",
    refreshToken: req.refreshToken ?? "",
    expiresIn: req.expiresIn ?? 3600,
    rememberMe: req.rememberMe ?? false,
  });
  return loadCurrentUser();
}

export async function logout(): Promise<void> {
  try {
    await authClient.logout({});
  } catch {
    /* logout is best-effort */
  }
  setCurrentUser(null);
}
