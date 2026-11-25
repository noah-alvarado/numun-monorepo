// Runs the SES feedback Lambda's handler (api/cmd/email-feedback) locally on
// one notification — what SNS would deliver after a bounce or complaint.

import { spawn } from "node:child_process";
import path from "node:path";

const apiDir = path.join(import.meta.dirname, "../../api");

const env = {
  ...process.env,
  DEV_MODE: "true",
  AWS_REGION: "us-east-2",
  AWS_ACCESS_KEY_ID: "local",
  AWS_SECRET_ACCESS_KEY: "local",
  AWS_ENDPOINT_URL_DYNAMODB: "http://localhost:8000",
  DDB_TABLE_NAME: "numun-test",
};

async function deliver(notification: unknown): Promise<void> {
  const child = spawn("go", ["run", "./cmd/email-feedback", "-local"], {
    cwd: apiDir,
    env,
    stdio: ["pipe", "pipe", "pipe"],
  });
  let output = "";
  child.stdout.on("data", (d) => (output += d));
  child.stderr.on("data", (d) => (output += d));
  child.stdin.end(JSON.stringify(notification));
  const code = await new Promise<number | null>((resolve) =>
    child.on("close", resolve),
  );
  if (code !== 0) {
    throw new Error(`email-feedback -local exited ${code}:\n${output}`);
  }
}

export function sesHardBounce(email: string): Promise<void> {
  return deliver({
    notificationType: "Bounce",
    mail: { messageId: `e2e-${Date.now()}` },
    bounce: {
      bounceType: "Permanent",
      bounceSubType: "General",
      bouncedRecipients: [{ emailAddress: email }],
    },
  });
}

export function sesSoftBounce(email: string): Promise<void> {
  return deliver({
    notificationType: "Bounce",
    mail: { messageId: `e2e-${Date.now()}` },
    bounce: {
      bounceType: "Transient",
      bounceSubType: "MailboxFull",
      bouncedRecipients: [{ emailAddress: email }],
    },
  });
}

export function sesComplaint(email: string): Promise<void> {
  return deliver({
    notificationType: "Complaint",
    mail: { messageId: `e2e-${Date.now()}` },
    complaint: {
      complainedRecipients: [{ emailAddress: email }],
      complaintFeedbackType: "abuse",
    },
  });
}
