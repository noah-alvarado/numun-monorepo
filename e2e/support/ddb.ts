// Direct DynamoDB Local writes for states the product can't reach on demand
// (e.g. an access token that has aged out). DDB Local accepts any
// well-formed SigV4 header, so no signing is needed.

const DDB_LOCAL_URL = "http://localhost:8000";
const TABLE = "numun-test";

async function updateItem(body: Record<string, unknown>): Promise<void> {
  const resp = await fetch(DDB_LOCAL_URL, {
    method: "POST",
    headers: {
      "Content-Type": "application/x-amz-json-1.0",
      "X-Amz-Target": "DynamoDB_20120810.UpdateItem",
      Authorization:
        "AWS4-HMAC-SHA256 Credential=local/20260101/us-east-2/dynamodb/aws4_request, SignedHeaders=host, Signature=0",
    },
    body: JSON.stringify({ TableName: TABLE, ...body }),
  });
  if (!resp.ok) {
    throw new Error(`DDB UpdateItem: ${resp.status} ${await resp.text()}`);
  }
}

// Marks the session's cached Cognito access token as expired, so the next
// request makes the API refresh it.
export async function expireAccessToken(sessionId: string): Promise<void> {
  await updateItem({
    Key: { PK: { S: `SESSION#${sessionId}` }, SK: { S: "META" } },
    UpdateExpression: "SET cachedAccessTokenExpiresAt = :t",
    ConditionExpression: "attribute_exists(PK)",
    ExpressionAttributeValues: { ":t": { S: "2000-01-01T00:00:00Z" } },
  });
}
