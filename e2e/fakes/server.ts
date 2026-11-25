// Fake Google Sheets + Cognito for the e2e suite, so flows that need those
// services run locally. Started by playwright.config.ts; run directly with
// `node fakes/server.ts`. No dependencies, in-memory state.
//
//   Google Sheets  GET /spreadsheets/d/<id>/htmlview and /export?format=csv
//                  (the API's DEV_SHEETS_ORIGIN proxy sends them here)
//   Cognito        POST / with X-Amz-Target: AWSCognitoIdentityProviderService.<Op>
//                  (portal VITE_COGNITO_ENDPOINT, API AWS_ENDPOINT_URL_COGNITO_IDENTITY_PROVIDER)
//   Control        /__control/* — tests register sheets and users, read
//                  emailed codes, and queue errors (see e2e/support/fakes.ts)
//
// Tokens are JWT-shaped but unsigned, with a "dev-" prefix the API's
// dev-bypass Exchange accepts (it reads the claims without verifying).

import { randomUUID } from "node:crypto";
import {
  createServer,
  type IncomingMessage,
  type ServerResponse,
} from "node:http";

const PORT = Number(process.env.FAKES_PORT ?? 4599);

type Tab = { name: string; gid: string; csv: string };
const sheets = new Map<string, Tab[]>();

type User = {
  sub: string;
  email: string;
  name: string;
  role: string;
  password: string;
  confirmed: boolean;
  mustChangePassword: boolean;
};
const users = new Map<string, User>();
const signUpCodes = new Map<string, string>();
const resetCodes = new Map<string, string>();
const challengeSessions = new Map<string, string>();
// Errors queued per operation; the next call to that operation fails.
const queuedErrors = new Map<string, { type: string; message: string }[]>();

class CognitoError extends Error {
  type: string;
  constructor(type: string, message: string) {
    super(message);
    this.type = type;
  }
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Body = Record<string, any>;

const key = (username: unknown) =>
  String(username ?? "")
    .trim()
    .toLowerCase();

const code = () => String(Math.floor(100000 + Math.random() * 900000));

// Mirrors the pool's policy in infra/base-data/template.yaml.
function checkPassword(pw: unknown) {
  const s = String(pw ?? "");
  if (s.length < 12 || !/[a-z]/.test(s) || !/[0-9]/.test(s)) {
    throw new CognitoError(
      "InvalidPasswordException",
      "Password did not conform with policy: Password not long enough",
    );
  }
}

const b64url = (v: unknown) =>
  Buffer.from(JSON.stringify(v)).toString("base64url");

function tokens(u: User) {
  const now = Math.floor(Date.now() / 1000);
  const jwt = (claims: Record<string, unknown>) =>
    `dev-${b64url({ alg: "none", typ: "JWT" })}.${b64url({ ...claims, iat: now, exp: now + 3600 })}.fake`;
  return {
    IdToken: jwt({
      sub: u.sub,
      email: u.email,
      name: u.name,
      "custom:role": u.role,
      token_use: "id",
    }),
    AccessToken: jwt({ sub: u.sub, token_use: "access", username: u.email }),
    RefreshToken: `dev-rt-${u.sub}`,
    ExpiresIn: 3600,
    TokenType: "Bearer",
  };
}

function attr(
  attrs: { Name: string; Value: string }[] | undefined,
  name: string,
) {
  return attrs?.find((a) => a.Name === name)?.Value ?? "";
}

function userAttributes(u: User) {
  return [
    { Name: "sub", Value: u.sub },
    { Name: "email", Value: u.email },
    { Name: "email_verified", Value: String(u.confirmed) },
    { Name: "name", Value: u.name },
    { Name: "custom:role", Value: u.role },
  ];
}

function mustFind(username: unknown): User {
  const u = users.get(key(username));
  if (!u) {
    throw new CognitoError(
      "UserNotFoundException",
      "Username/client id combination not found.",
    );
  }
  return u;
}

const delivery = (email: string) => ({
  CodeDeliveryDetails: {
    DeliveryMedium: "EMAIL",
    AttributeName: "email",
    Destination: email,
  },
});

const cognito: Record<string, (b: Body) => unknown> = {
  SignUp(b) {
    const email = key(b.Username);
    if (users.has(email)) {
      throw new CognitoError(
        "UsernameExistsException",
        "An account with the given email already exists.",
      );
    }
    checkPassword(b.Password);
    const u: User = {
      sub: randomUUID(),
      email,
      name: attr(b.UserAttributes, "name"),
      role: "advisor",
      password: b.Password,
      confirmed: false,
      mustChangePassword: false,
    };
    users.set(email, u);
    signUpCodes.set(email, code());
    return { UserConfirmed: false, UserSub: u.sub, ...delivery(email) };
  },

  ConfirmSignUp(b) {
    const u = mustFind(b.Username);
    if (signUpCodes.get(u.email) !== String(b.ConfirmationCode).trim()) {
      throw new CognitoError(
        "CodeMismatchException",
        "Invalid verification code provided, please try again.",
      );
    }
    u.confirmed = true;
    signUpCodes.delete(u.email);
    return {};
  },

  ResendConfirmationCode(b) {
    const u = mustFind(b.Username);
    signUpCodes.set(u.email, code());
    return delivery(u.email);
  },

  InitiateAuth(b) {
    if (b.AuthFlow === "REFRESH_TOKEN_AUTH") {
      const sub = String(b.AuthParameters?.REFRESH_TOKEN ?? "").replace(
        /^dev-rt-/,
        "",
      );
      const u = [...users.values()].find((x) => x.sub === sub);
      if (!u) {
        throw new CognitoError(
          "NotAuthorizedException",
          "Invalid Refresh Token",
        );
      }
      const { IdToken, AccessToken, ExpiresIn, TokenType } = tokens(u);
      return {
        AuthenticationResult: { IdToken, AccessToken, ExpiresIn, TokenType },
        ChallengeParameters: {},
      };
    }
    if (b.AuthFlow !== "USER_PASSWORD_AUTH") {
      throw new CognitoError(
        "InvalidParameterException",
        `Auth flow ${b.AuthFlow} is not enabled for this client.`,
      );
    }
    const u = users.get(key(b.AuthParameters?.USERNAME));
    // PreventUserExistenceErrors: unknown users look like a wrong password.
    if (!u || u.password !== b.AuthParameters?.PASSWORD) {
      throw new CognitoError(
        "NotAuthorizedException",
        "Incorrect username or password.",
      );
    }
    if (!u.confirmed) {
      throw new CognitoError(
        "UserNotConfirmedException",
        "User is not confirmed.",
      );
    }
    if (u.mustChangePassword) {
      const session = randomUUID();
      challengeSessions.set(session, u.email);
      return {
        ChallengeName: "NEW_PASSWORD_REQUIRED",
        Session: session,
        ChallengeParameters: {
          USER_ID_FOR_SRP: u.email,
          requiredAttributes: "[]",
          userAttributes: JSON.stringify({ email: u.email, name: u.name }),
        },
      };
    }
    return { AuthenticationResult: tokens(u), ChallengeParameters: {} };
  },

  RespondToAuthChallenge(b) {
    const email = challengeSessions.get(b.Session);
    if (!email || b.ChallengeName !== "NEW_PASSWORD_REQUIRED") {
      throw new CognitoError(
        "NotAuthorizedException",
        "Invalid session for the user, session is expired.",
      );
    }
    const u = mustFind(email);
    checkPassword(b.ChallengeResponses?.NEW_PASSWORD);
    u.password = b.ChallengeResponses.NEW_PASSWORD;
    u.mustChangePassword = false;
    challengeSessions.delete(b.Session);
    return { AuthenticationResult: tokens(u), ChallengeParameters: {} };
  },

  ForgotPassword(b) {
    const email = key(b.Username);
    // PreventUserExistenceErrors: unknown users get the same response.
    if (users.has(email)) resetCodes.set(email, code());
    return delivery(email);
  },

  ConfirmForgotPassword(b) {
    const email = key(b.Username);
    const expected = resetCodes.get(email);
    if (!expected || expected !== String(b.ConfirmationCode).trim()) {
      throw new CognitoError(
        "CodeMismatchException",
        "Invalid verification code provided, please try again.",
      );
    }
    checkPassword(b.Password);
    mustFind(email).password = b.Password;
    resetCodes.delete(email);
    return {};
  },

  AdminCreateUser(b) {
    const email = key(b.Username);
    if (users.has(email)) {
      throw new CognitoError(
        "UsernameExistsException",
        "User account already exists",
      );
    }
    const u: User = {
      sub: randomUUID(),
      email,
      name: attr(b.UserAttributes, "name"),
      role: attr(b.UserAttributes, "custom:role") || "advisor",
      password: `temp-${code()}-password`,
      confirmed: true,
      mustChangePassword: true,
    };
    users.set(email, u);
    return {
      User: {
        Username: email,
        Attributes: userAttributes(u),
        Enabled: true,
        UserStatus: "FORCE_CHANGE_PASSWORD",
      },
    };
  },

  AdminGetUser(b) {
    const u = mustFind(b.Username);
    return {
      Username: u.email,
      UserAttributes: userAttributes(u),
      Enabled: true,
      UserStatus: u.confirmed ? "CONFIRMED" : "UNCONFIRMED",
    };
  },

  RevokeToken() {
    return {};
  },
};

async function readJSON(req: IncomingMessage): Promise<Body> {
  const chunks: Buffer[] = [];
  for await (const c of req) chunks.push(c as Buffer);
  const raw = Buffer.concat(chunks).toString("utf8");
  return raw ? JSON.parse(raw) : {};
}

function send(
  res: ServerResponse,
  status: number,
  body: unknown,
  type = "application/json",
) {
  res.writeHead(status, {
    "Content-Type": type,
    // The portal calls Cognito straight from the browser.
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Headers": "*",
    "Access-Control-Allow-Methods": "GET, POST, DELETE, OPTIONS",
  });
  res.end(typeof body === "string" ? body : JSON.stringify(body));
}

// The shape of Google's /htmlview tab list (see api/internal/parse/sheets.go).
function htmlview(tabs: Tab[]): string {
  const items = tabs
    .map(
      (t) =>
        `items.push({name: ${JSON.stringify(t.name)}, pageUrl: "https:\\/\\/docs.google.com\\/spreadsheets\\/d\\/x\\/htmlview\\/sheet?headers\\x3dtrue&gid=${t.gid}", gid: "${t.gid}",initialSheet: false});`,
    )
    .join("");
  return `<!DOCTYPE html><html><body><script>var items = [];${items}</script></body></html>`;
}

async function control(req: IncomingMessage, res: ServerResponse, url: URL) {
  const path = url.pathname.replace(/^\/__control/, "");
  const body = req.method === "POST" ? await readJSON(req) : {};

  if (path === "/health") return send(res, 200, { ok: true });

  // POST /sheets { id, tabs: [{ name, gid, csv }] }
  if (path === "/sheets" && req.method === "POST") {
    sheets.set(body.id, body.tabs);
    return send(res, 200, {});
  }

  // POST /users { email, password, sub?, name?, role?, confirmed?,
  // mustChangePassword? } — create or replace.
  if (path === "/users" && req.method === "POST") {
    const email = key(body.email);
    const u: User = {
      sub: body.sub ?? users.get(email)?.sub ?? randomUUID(),
      email,
      name: body.name ?? "",
      role: body.role ?? "advisor",
      password: body.password,
      confirmed: body.confirmed ?? true,
      mustChangePassword: body.mustChangePassword ?? false,
    };
    users.set(email, u);
    return send(res, 200, { sub: u.sub });
  }

  // GET /users?email= — includes an invited user's temporary password.
  if (path === "/users" && req.method === "GET") {
    const u = users.get(key(url.searchParams.get("email")));
    return u ? send(res, 200, u) : send(res, 404, { error: "no such user" });
  }

  // GET /codes?email=&kind=signup|reset — the code Cognito would email.
  if (path === "/codes") {
    const email = key(url.searchParams.get("email"));
    const codes =
      url.searchParams.get("kind") === "reset" ? resetCodes : signUpCodes;
    const c = codes.get(email);
    return c
      ? send(res, 200, { code: c })
      : send(res, 404, { error: "no code" });
  }

  // POST /errors { op, type, message } — the next <op> call fails.
  // DELETE /errors clears the queue.
  if (path === "/errors" && req.method === "POST") {
    const q = queuedErrors.get(body.op) ?? [];
    q.push({ type: body.type, message: body.message });
    queuedErrors.set(body.op, q);
    return send(res, 200, {});
  }
  if (path === "/errors" && req.method === "DELETE") {
    queuedErrors.clear();
    return send(res, 200, {});
  }

  send(res, 404, { error: `unknown control path ${path}` });
}

async function handle(req: IncomingMessage, res: ServerResponse) {
  const url = new URL(req.url ?? "/", `http://${req.headers.host}`);
  if (req.method === "OPTIONS") return send(res, 204, "");
  if (url.pathname.startsWith("/__control")) return control(req, res, url);

  const sheet = url.pathname.match(
    /^\/spreadsheets\/d\/([^/]+)\/(htmlview|export)$/,
  );
  if (sheet && req.method === "GET") {
    const tabs = sheets.get(sheet[1]);
    if (!tabs) return send(res, 404, "Not found", "text/html");
    if (sheet[2] === "htmlview") {
      return send(res, 200, htmlview(tabs), "text/html");
    }
    const gid = url.searchParams.get("gid") ?? "0";
    const tab = tabs.find((t) => t.gid === gid);
    if (!tab) return send(res, 400, "Bad gid", "text/html");
    return send(res, 200, tab.csv, "text/csv");
  }

  const target = String(req.headers["x-amz-target"] ?? "");
  if (
    req.method === "POST" &&
    target.startsWith("AWSCognitoIdentityProviderService.")
  ) {
    const op = target.split(".")[1];
    const body = await readJSON(req);
    try {
      const queued = queuedErrors.get(op)?.shift();
      if (queued) throw new CognitoError(queued.type, queued.message);
      const handler = cognito[op];
      if (!handler) {
        throw new CognitoError(
          "InvalidActionException",
          `fake Cognito doesn't implement ${op}`,
        );
      }
      return send(res, 200, handler(body), "application/x-amz-json-1.1");
    } catch (err) {
      if (!(err instanceof CognitoError)) throw err;
      res.setHeader("x-amzn-ErrorType", `${err.type}:`);
      return send(
        res,
        400,
        { __type: err.type, message: err.message },
        "application/x-amz-json-1.1",
      );
    }
  }

  send(res, 404, { error: "not found" });
}

createServer((req, res) => {
  handle(req, res).catch((err) => send(res, 500, { error: String(err) }));
}).listen(PORT, "127.0.0.1", () => {
  console.log(`fakes listening on http://127.0.0.1:${PORT}`);
});
