// Active-conference snapshot from /content/_generated/active-conference.json.
// The deploy workflow (.github/workflows/site.yml) fetches
// PublicService.GetActiveConference before build and writes the file. When
// running locally (where the file may not exist), this falls back to the
// "no active conference" shape so the build still succeeds.
//
// See docs/subsystems/CMS_CONTENT_MODEL.md §9.

import fs from "node:fs";
import path from "node:path";
import url from "node:url";

export type ActiveConference = {
  conferenceId: string | null;
  name?: string;
  editionNumber?: number;
  year?: number;
  startsAt?: string;
  endsAt?: string;
  registrationStatus?: string;
  themeMetadata?: Record<string, string>;
};

const here = path.dirname(url.fileURLToPath(import.meta.url));
const dataPath = path.resolve(
  here,
  "../../../content/_generated/active-conference.json",
);

let data: ActiveConference = { conferenceId: null };
try {
  const raw = fs.readFileSync(dataPath, "utf8");
  const parsed = JSON.parse(raw);
  // Workflow writes either the full ActiveConferenceSummary (under .conference)
  // or {"conferenceId": null}; accept both shapes.
  data =
    parsed && parsed.conferenceId !== undefined
      ? parsed
      : { conferenceId: null };
} catch {
  // File missing → leave fallback in place.
}

export default data;
