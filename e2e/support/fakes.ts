// Drives fakes/server.ts (fake Google Sheets + Cognito) from tests.

import { FAKES_URL } from "../playwright.config";

async function control<T = unknown>(
  method: "GET" | "POST" | "DELETE",
  path: string,
  body?: unknown,
): Promise<T> {
  const resp = await fetch(`${FAKES_URL}/__control${path}`, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!resp.ok) {
    throw new Error(
      `fakes ${method} ${path}: ${resp.status} ${await resp.text()}`,
    );
  }
  return (await resp.json()) as T;
}

export type SheetTab = { name: string; gid: string; csv: string };

// Publishes a fake Google Sheet; returns the URL an advisor would paste.
export async function publishSheet(
  id: string,
  tabs: SheetTab[],
): Promise<string> {
  await control("POST", "/sheets", { id, tabs });
  return `https://docs.google.com/spreadsheets/d/${id}/edit`;
}

export type CognitoUser = {
  email: string;
  password: string;
  sub?: string;
  name?: string;
  role?: "advisor" | "staff-staffer" | "staff-admin";
  confirmed?: boolean;
  mustChangePassword?: boolean;
};

// Creates or replaces a user in the fake Cognito pool; returns its sub.
export async function putCognitoUser(user: CognitoUser): Promise<string> {
  const { sub } = await control<{ sub: string }>("POST", "/users", user);
  return sub;
}

export async function getCognitoUser(
  email: string,
): Promise<CognitoUser & { sub: string }> {
  return control("GET", `/users?email=${encodeURIComponent(email)}`);
}

// The verification or reset code Cognito would have emailed.
export async function emailedCode(
  email: string,
  kind: "signup" | "reset",
): Promise<string> {
  const { code } = await control<{ code: string }>(
    "GET",
    `/codes?email=${encodeURIComponent(email)}&kind=${kind}`,
  );
  return code;
}

// Makes the next call to a Cognito operation fail with the given error.
export async function failNextCognitoCall(
  op: string,
  type: string,
  message: string,
): Promise<void> {
  await control("POST", "/errors", { op, type, message });
}

export async function clearCognitoErrors(): Promise<void> {
  await control("DELETE", "/errors");
}
