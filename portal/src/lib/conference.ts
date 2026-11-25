import { createResource, type Resource } from "solid-js";

import { conferenceClient } from "@/lib/api";
import { activeConferenceSignal } from "@/lib/session";

// conferenceLabel resolves a conference id to "Name (year)" for page headers,
// reusing the session's active conference when it matches. Resolves to "" if
// the lookup fails, so callers never fall back to showing the raw id.
export function conferenceLabel(conferenceId: () => string): Resource<string> {
  const [active] = activeConferenceSignal;
  const [label] = createResource(conferenceId, async (id) => {
    const a = active();
    if (a?.conferenceId === id) return `${a.name} (${a.year})`;
    try {
      const { conference } = await conferenceClient.getConference({
        conferenceId: id,
      });
      return conference ? `${conference.name} (${conference.year})` : "";
    } catch {
      return "";
    }
  });
  return label;
}
