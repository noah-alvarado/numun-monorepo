// Exports — CSV downloads of the active conference's delegates, payments and
// assignments (API.md §12). The server scopes each file to the caller:
// admins get everything, staffers the delegations they oversee, advisors
// their own delegation (approved assignments only).

import { For, Show } from "solid-js";

import { exportUrls } from "@/lib/api";
import { activeConferenceSignal, userSignal } from "@/lib/session";
import { User_Role } from "@/gen/numun/v1/users_pb";
import PageHeader from "@/components/PageHeader";

const FILES = [
  {
    kind: "delegates",
    url: exportUrls.delegatesCsv,
    about: "Names, emails, experience and check-in times.",
  },
  {
    kind: "payments",
    url: exportUrls.paymentsCsv,
    about: "Every charge and payment on the ledger.",
  },
  {
    kind: "assignments",
    url: exportUrls.assignmentsCsv,
    about: "Committee and position for each delegate.",
  },
] as const;

export default function Exports() {
  const [user] = userSignal;
  const [conference] = activeConferenceSignal;

  const scope = () => {
    switch (user()?.role) {
      case User_Role.STAFF_ADMIN:
        return "Files cover every delegation in the conference.";
      case User_Role.STAFF_STAFFER:
        return "Files cover the delegations you're assigned to.";
      default:
        return "Files cover your delegation. Assignments appear once NUMUN staff approve them.";
    }
  };

  return (
    <main class="mx-auto max-w-3xl px-6 py-8">
      <PageHeader title="Exports" subtitle={conference()?.name} />
      <Show
        when={conference()}
        fallback={
          <p class="mt-6 rounded bg-amber-50 p-3 text-sm text-amber-900">
            No active conference.
          </p>
        }
      >
        {(c) => (
          <section class="mt-6 space-y-3 text-sm">
            <p class="text-nu-purple-700">
              {scope()} Spreadsheet apps open them directly.
            </p>
            <ul class="divide-y divide-nu-purple-100 rounded border border-nu-purple-200 bg-white">
              <For each={FILES}>
                {(f) => (
                  <li class="flex items-center justify-between gap-4 p-4">
                    <div>
                      <div class="font-semibold capitalize text-nu-purple">
                        {f.kind}
                      </div>
                      <div class="text-nu-purple-700">{f.about}</div>
                    </div>
                    <a
                      href={f.url(c().conferenceId)}
                      download=""
                      class="rounded border border-nu-purple-300 px-3 py-1 text-nu-purple-700 hover:bg-nu-purple-50"
                    >
                      Export {f.kind}.csv
                    </a>
                  </li>
                )}
              </For>
            </ul>
          </section>
        )}
      </Show>
    </main>
  );
}
