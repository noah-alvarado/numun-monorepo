import { ConnectError } from "@connectrpc/connect";

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
