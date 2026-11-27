import { Code, ConnectError } from "@connectrpc/connect";

// connectMessage returns an error's user-facing text. For ConnectErrors that
// is the server's message without the "[code]" prefix ConnectError.message adds.
export function connectMessage(err: unknown): string | null {
  if (err instanceof ConnectError) return sentenceCase(err.rawMessage);
  if (err instanceof Error) return err.message;
  return null;
}

// Server messages are lowercase ("file has no data rows"); show them as
// sentences.
function sentenceCase(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// isConflict reports an optimistic-lock failure: someone saved the record
// after this page loaded it.
export function isConflict(err: unknown): boolean {
  return err instanceof ConnectError && err.code === Code.Aborted;
}

// conflictMessage is shown after a conflict once the page has refetched, so
// the user sees the other change before deciding whether to redo theirs.
export function conflictMessage(what: string): string {
  return `Someone else changed ${what} first. The page now shows their change; redo yours if it's still needed.`;
}
