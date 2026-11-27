// Fake Google Sheets + Cognito for the e2e suite, so flows that need those
// services run locally. Started by playwright.config.ts; run directly with
// `node fakes/server.ts`. No dependencies, in-memory state.
//
//   Google Sheets  GET /spreadsheets/d/<id>/htmlview and /export?format=csv
//                  (the API's DEV_SHEETS_ORIGIN proxy sends them here)
//   Cognito        POST / with X-Amz-Target: AWSCognitoIdentityProviderService.<Op>
//                  (portal VITE_COGNITO_ENDPOINT, API AWS_ENDPOINT_URL_COGNITO_IDENTITY_PROVIDER)
//   GitHub         /github/* — the App token, branch head, contents and
//                  createCommitOnBranch calls the API's award CMS sync makes
//                  (DEV_CMS_GITHUB_API_URL); commits land in memory
//   Control        /__control/* — tests register sheets and users, read
//                  emailed codes and committed files, and queue errors (see
//                  e2e/support/fakes.ts)
//
// Tokens are real RS256 JWTs; the API verifies ID tokens against this
// server's JWKS (COGNITO_ISSUER_URL), exactly as it does Cognito's.

import { execFileSync } from "node:child_process";
import {
  generateKeyPairSync,
  randomUUID,
  sign,
  type KeyObject,
} from "node:crypto";
import {
  createServer,
  type IncomingMessage,
  type ServerResponse,
} from "node:http";

const PORT = Number(process.env.FAKES_PORT ?? 4599);
const POOL_ID = "us-east-2_e2eFake";
const CLIENT_ID = "e2efakeclient";
const ISSUER = `http://127.0.0.1:${PORT}/${POOL_ID}`;

const signingKey = generateKeyPairSync("rsa", { modulusLength: 2048 });
const KID = "fake-1";
// Signs with the right kid but the wrong key, for forgery tests.
const forgeryKey = generateKeyPairSync("rsa", {
  modulusLength: 2048,
}).privateKey;

type Tab = { name: string; gid: string; csv: string };
const sheets = new Map<string, Tab[]>();

type User = {
  sub: string;
  email: string;
  name: string;
  phone: string;
  role: string;
  password: string;
  confirmed: boolean;
  mustChangePassword: boolean;
};
const users = new Map<string, User>();
const signUpCodes = new Map<string, string>();
const resetCodes = new Map<string, string>();
const challengeSessions = new Map<string, string>();
// Live refresh tokens → sub. Revoking a user drops theirs.
const refreshTokens = new Map<string, string>();
const refreshCalls = new Map<string, number>();
// Errors queued per operation; the next call to that operation fails.
const queuedErrors = new Map<string, { type: string; message: string }[]>();

// The content repo as the CMS sync has committed it: path → file contents.
const repoFiles = new Map<string, string>();
let repoHead = "0".repeat(40);
// GitHub calls left to fail with a 502 (each sync retries up to 3 times).
let githubFailures = 0;

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

function signJwt(
  claims: Record<string, unknown>,
  opts: { key?: KeyObject; lifetime?: number } = {},
) {
  const now = Math.floor(Date.now() / 1000);
  const head = b64url({ alg: "RS256", typ: "JWT", kid: KID });
  const body = b64url({
    iss: ISSUER,
    iat: now,
    exp: now + (opts.lifetime ?? 3600),
    ...claims,
  });
  const sig = sign(
    "sha256",
    Buffer.from(`${head}.${body}`),
    opts.key ?? signingKey.privateKey,
  ).toString("base64url");
  return `${head}.${body}.${sig}`;
}

function idClaims(u: User) {
  return {
    sub: u.sub,
    aud: CLIENT_ID,
    email: u.email,
    name: u.name,
    "custom:role": u.role,
    token_use: "id",
  };
}

function tokens(u: User) {
  const refresh = `rt.${randomUUID()}`;
  refreshTokens.set(refresh, u.sub);
  return {
    IdToken: signJwt(idClaims(u)),
    AccessToken: signJwt({
      sub: u.sub,
      client_id: CLIENT_ID,
      token_use: "access",
      username: u.email,
    }),
    RefreshToken: refresh,
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

// Runs the real post-confirmation trigger (api/cmd/cognito-post-confirmation
// -local), synchronously like Cognito, when POST_CONFIRMATION_BIN is set.
function postConfirmation(triggerSource: string, u: User) {
  const bin = process.env.POST_CONFIRMATION_BIN;
  if (!bin) return;
  const event = {
    version: "1",
    triggerSource,
    region: "us-east-2",
    userPoolId: "us-east-2_e2eFake",
    userName: u.email,
    callerContext: { awsSdkVersion: "fake", clientId: "e2efakeclient" },
    request: {
      userAttributes: {
        sub: u.sub,
        email: u.email,
        email_verified: "true",
        name: u.name,
        phone_number: u.phone,
        "custom:role": u.role,
      },
    },
    response: {},
  };
  execFileSync(bin, ["-local"], {
    input: JSON.stringify(event),
    stdio: ["pipe", "ignore", "inherit"],
  });
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
      phone: attr(b.UserAttributes, "phone_number"),
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
    postConfirmation("PostConfirmation_ConfirmSignUp", u);
    return {};
  },

  ResendConfirmationCode(b) {
    const u = mustFind(b.Username);
    signUpCodes.set(u.email, code());
    return delivery(u.email);
  },

  InitiateAuth(b) {
    if (b.AuthFlow === "REFRESH_TOKEN_AUTH") {
      const sub = refreshTokens.get(String(b.AuthParameters?.REFRESH_TOKEN));
      if (sub) refreshCalls.set(sub, (refreshCalls.get(sub) ?? 0) + 1);
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
    const u = mustFind(email);
    u.password = b.Password;
    resetCodes.delete(email);
    postConfirmation("PostConfirmation_ConfirmForgotPassword", u);
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
      phone: "",
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

  RevokeToken(b) {
    refreshTokens.delete(String(b.Token));
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
      phone: "",
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

  // POST /revoke { email } — invalidates the user's refresh tokens, as if
  // they were signed out everywhere or disabled.
  if (path === "/revoke" && req.method === "POST") {
    const u = mustFind(body.email);
    for (const [t, sub] of refreshTokens)
      if (sub === u.sub) refreshTokens.delete(t);
    return send(res, 200, {});
  }

  // GET /refreshes?email= — how many REFRESH_TOKEN_AUTH calls the user's
  // tokens have seen.
  if (path === "/refreshes") {
    const u = mustFind(url.searchParams.get("email"));
    return send(res, 200, { count: refreshCalls.get(u.sub) ?? 0 });
  }

  // POST /id-token { email, variant?: "forged" | "expired" | "wrong-audience" }
  // — an ID token for the user, possibly invalid, to exercise verification.
  if (path === "/id-token" && req.method === "POST") {
    const u = mustFind(body.email);
    const claims: Record<string, unknown> = idClaims(u);
    if (body.variant === "wrong-audience") claims.aud = "some-other-client";
    const token = signJwt(claims, {
      key: body.variant === "forged" ? forgeryKey : undefined,
      lifetime: body.variant === "expired" ? -600 : undefined,
    });
    return send(res, 200, { token });
  }

  // GET /github/files?path= — a file the CMS sync committed.
  if (path === "/github/files") {
    const f = repoFiles.get(url.searchParams.get("path") ?? "");
    return f === undefined
      ? send(res, 404, { error: "no such file" })
      : send(res, 200, { content: f });
  }

  // POST /github/fail { count } — the next <count> GitHub calls fail.
  if (path === "/github/fail" && req.method === "POST") {
    githubFailures = body.count;
    return send(res, 200, {});
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

// The slice of GitHub's REST + GraphQL API that api/internal/cms/git.go uses.
async function github(req: IncomingMessage, res: ServerResponse, url: URL) {
  const path = url.pathname.replace(/^\/github/, "");
  if (githubFailures > 0) {
    githubFailures -= 1;
    return send(res, 502, { message: "Server Error (fake)" });
  }
  if (/^\/app\/installations\/[^/]+\/access_tokens$/.test(path)) {
    const expires = new Date(Date.now() + 3600_000).toISOString();
    return send(res, 201, { token: "ghs_fake", expires_at: expires });
  }
  if (/^\/repos\/[^/]+\/[^/]+\/git\/ref\/heads\/[^/]+$/.test(path)) {
    return send(res, 200, { object: { sha: repoHead } });
  }
  const contents = path.match(/^\/repos\/[^/]+\/[^/]+\/contents\/(.+)$/);
  if (contents && req.method === "GET") {
    return repoFiles.has(contents[1])
      ? send(res, 200, { sha: `blob-${contents[1]}` })
      : send(res, 404, { message: "Not Found" });
  }
  if (path === "/graphql" && req.method === "POST") {
    const { input } = (await readJSON(req)).variables;
    if (input.expectedHeadOid !== repoHead) {
      return send(res, 200, { errors: [{ message: "head moved" }] });
    }
    for (const a of input.fileChanges.additions ?? []) {
      repoFiles.set(a.path, Buffer.from(a.contents, "base64").toString("utf8"));
    }
    for (const d of input.fileChanges.deletions ?? []) repoFiles.delete(d.path);
    repoHead = randomUUID().replaceAll("-", "").padEnd(40, "0");
    return send(res, 200, {
      data: { createCommitOnBranch: { commit: { oid: repoHead } } },
    });
  }
  send(res, 404, { message: "Not Found" });
}

async function handle(req: IncomingMessage, res: ServerResponse) {
  const url = new URL(req.url ?? "/", `http://${req.headers.host}`);
  if (req.method === "OPTIONS") return send(res, 204, "");
  if (url.pathname.startsWith("/__control")) return control(req, res, url);

  if (url.pathname === `/${POOL_ID}/.well-known/jwks.json`) {
    const jwk = signingKey.publicKey.export({ format: "jwk" });
    return send(res, 200, {
      keys: [{ ...jwk, kid: KID, alg: "RS256", use: "sig" }],
    });
  }

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

  if (url.pathname.startsWith("/github/")) return github(req, res, url);

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
