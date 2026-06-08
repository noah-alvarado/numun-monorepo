// In-memory current-user state for the SolidJS app shell. We never store
// auth tokens here — the session is a cookie the browser holds; this module
// only caches the User profile returned by UserService.GetMe so the app can
// reactively render.

import { createSignal, type Signal } from "solid-js";
import { userClient, authClient, publicClient } from "./api";
import type { User } from "@/gen/numun/v1/users_pb";
import type { ExchangeRequest } from "@/gen/numun/v1/auth_pb";
import type { ActiveConferenceSummary } from "@/gen/numun/v1/public_pb";

const [currentUser, setCurrentUser] = createSignal<User | null>(null);
const [loading, setLoading] = createSignal(false);
const [activeConference, setActiveConference] =
  createSignal<ActiveConferenceSummary | null>(null);
const [activeConferenceLoaded, setActiveConferenceLoaded] = createSignal(false);

export const userSignal: Signal<User | null> = [currentUser, setCurrentUser];
export const loadingSignal: Signal<boolean> = [loading, setLoading];
export const activeConferenceSignal: Signal<ActiveConferenceSummary | null> = [
  activeConference,
  setActiveConference,
];
export const activeConferenceLoadedSignal: Signal<boolean> = [
  activeConferenceLoaded,
  setActiveConferenceLoaded,
];

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

/**
 * loadActiveConference calls PublicService.GetActiveConference and caches the
 * result. Safe to call repeatedly; the cached signal is the source of truth
 * for advisor/admin screens needing to know "which conference are we in?".
 */
export async function loadActiveConference(): Promise<ActiveConferenceSummary | null> {
  try {
    const resp = await publicClient.getActiveConference({});
    setActiveConference(resp.conference ?? null);
    return resp.conference ?? null;
  } finally {
    setActiveConferenceLoaded(true);
  }
}

export async function logout(): Promise<void> {
  try {
    await authClient.logout({});
  } catch {
    /* logout is best-effort */
  }
  setCurrentUser(null);
}
