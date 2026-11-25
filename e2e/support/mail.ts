// Reads mail the app sent, from Mailpit's API (docker compose; the API and
// email worker deliver there via DEV_SMTP_ADDR).

import { expect } from "@playwright/test";

const MAILPIT = "http://localhost:8025/api/v1";

export type Mail = {
  id: string;
  subject: string;
  from: string;
  to: string[];
  text: string;
  html: string;
  headers: Record<string, string[]>;
};

type Summary = { ID: string; Subject: string; Created: string };

async function get<T>(path: string): Promise<T> {
  const resp = await fetch(`${MAILPIT}${path}`);
  if (!resp.ok) throw new Error(`mailpit ${path}: ${resp.status}`);
  return (await resp.json()) as T;
}

async function read(id: string): Promise<Mail> {
  const m = await get<{
    ID: string;
    Subject: string;
    From: { Address: string; Name: string };
    To: { Address: string }[];
    Text: string;
    HTML: string;
  }>(`/message/${id}`);
  const headers = await get<Record<string, string[]>>(`/message/${id}/headers`);
  return {
    id: m.ID,
    subject: m.Subject,
    from: m.From.Name ? `${m.From.Name} <${m.From.Address}>` : m.From.Address,
    to: m.To.map((t) => t.Address),
    text: m.Text,
    html: m.HTML,
    headers,
  };
}

async function search(
  to: string,
  subject?: RegExp,
  containing?: string,
): Promise<Summary[]> {
  const terms = [`to:"${to}"`];
  if (containing) terms.push(`"${containing}"`);
  const q = encodeURIComponent(terms.join(" "));
  const { messages } = await get<{ messages: Summary[] }>(
    `/search?query=${q}&limit=50`,
  );
  return messages.filter((m) => !subject || subject.test(m.Subject));
}

// Waits for the newest email to `to` whose subject matches and, if given,
// whose body contains `containing` (shared inboxes keep older runs' mail).
export async function waitForMail(
  to: string,
  subject: RegExp,
  opts: { containing?: string; timeout?: number } = {},
): Promise<Mail> {
  const { containing, timeout = 15_000 } = opts;
  let found: Summary | undefined;
  await expect
    .poll(
      async () => {
        found = (await search(to, subject, containing))[0];
        return !!found;
      },
      { timeout, message: `email to ${to} matching ${subject}` },
    )
    .toBe(true);
  return read(found!.ID);
}

// Asserts no email to `to` matching subject arrives within `wait` ms.
export async function expectNoMail(
  to: string,
  subject: RegExp,
  wait = 4_000,
): Promise<void> {
  await new Promise((r) => setTimeout(r, wait));
  expect(await search(to, subject), `email to ${to}`).toHaveLength(0);
}

// Opens the email's HTML in the page, so the step screenshot shows the
// message as recipients see it.
export async function showMail(
  page: import("@playwright/test").Page,
  mail: Mail,
): Promise<void> {
  await page.goto(`http://localhost:8025/view/${mail.id}.html`);
}
