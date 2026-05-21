export const API_BASE = "http://localhost:8080";
const API_KEY = "test-api-key-12345";
const TOKEN_KEY = "pki_token";

// ---- Token helpers ----

export function getAuthToken(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem(TOKEN_KEY);
}

export function setAuthToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token);
}

export function clearAuthToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

// ---- Headers: JWT if present, otherwise API_KEY fallback ----

function authHeaders(includeContentType = true): HeadersInit {
  const token = getAuthToken();
  const bearer = token ?? API_KEY;
  const h: HeadersInit = { Authorization: `Bearer ${bearer}` };
  if (includeContentType) h["Content-Type"] = "application/json";
  return h;
}

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(`HTTP ${res.status}: ${text}`);
  }
  const json = await res.json();
  return (json.data ?? json) as T;
}

// ---- Types ----

export type User = {
  id: string;
  email: string;
  name: string;
  role: string;
  tenant_id: string;
};

export type DocumentStatus =
  | "draft"
  | "pending"
  | "partially_signed"
  | "signed"
  | "rejected";

export type Signature = {
  id: string;
  signer_name: string;
  signer_iin: string | null;
  org_name: string | null;
  signer_bin: string | null;
  signer_type: string;
  basis: string | null;
  role: string;
  cert_serial: string;
  cert_not_before: string;
  cert_not_after: string;
  ca_name: string;
  ocsp_status: string;
  tsp_time: string | null;
  sha256_hash: string;
  sign_format: string;
  qr_url: string;
  signed_at: string;
  sequence_num: number;
  version_number: number;
};

export type Document = {
  id: string;
  title: string | null;
  status: DocumentStatus;
  current_version: number;
  s3_key_current: string;
  signatures: Signature[] | null;
  created_at: string;
  updated_at: string;
};

export type SignResult = {
  signature_id: string;
  signed_document_url: string;
  signature: Signature;
};

export type DemoUploadResult = {
  document_id: string;
  title: string;
  sha256_hash: string;
  status: string;
};

// ---- Auth API ----

export async function register(
  email: string,
  password: string,
  name: string
): Promise<{ user: User; token: string }> {
  const res = await fetch(`${API_BASE}/auth/register`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password, name }),
  });
  return handleResponse(res);
}

export async function login(
  email: string,
  password: string
): Promise<{ user: User; token: string }> {
  const res = await fetch(`${API_BASE}/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  return handleResponse(res);
}

export async function logout(): Promise<void> {
  await fetch(`${API_BASE}/auth/logout`, {
    method: "POST",
    headers: authHeaders(),
  }).catch(() => {});
  clearAuthToken();
}

export async function me(): Promise<User> {
  const res = await fetch(`${API_BASE}/auth/me`, {
    headers: authHeaders(),
    cache: "no-store",
  });
  return handleResponse(res);
}

// ---- Documents API ----

export async function registerDocument(
  s3Key: string,
  title: string
): Promise<{ id: string; status: string }> {
  const res = await fetch(`${API_BASE}/api/v1/documents`, {
    method: "POST",
    headers: authHeaders(),
    body: JSON.stringify({ s3_key: s3Key, title }),
  });
  return handleResponse(res);
}

export async function getDocument(id: string): Promise<Document> {
  const res = await fetch(`${API_BASE}/api/v1/documents/${id}`, {
    headers: authHeaders(),
    cache: "no-store",
  });
  return handleResponse(res);
}

export async function signDocument(
  id: string,
  cms: string,
  role: string
): Promise<SignResult> {
  const res = await fetch(`${API_BASE}/api/v1/documents/${id}/sign`, {
    method: "POST",
    headers: authHeaders(),
    body: JSON.stringify({ cms, role }),
  });
  return handleResponse(res);
}

export async function demoUpload(
  file: File,
  title: string
): Promise<DemoUploadResult> {
  const form = new FormData();
  form.append("file", file);
  form.append("title", title);
  const res = await fetch(`${API_BASE}/api/v1/upload`, {
    method: "POST",
    headers: { Authorization: `Bearer ${getAuthToken() ?? API_KEY}` },
    body: form,
  });
  return handleResponse(res);
}

export async function demoSign(
  documentId: string,
  role: string
): Promise<SignResult> {
  const res = await fetch(
    `${API_BASE}/api/demo/sign/${documentId}?role=${encodeURIComponent(role)}`,
    {
      method: "POST",
      headers: authHeaders(),
    }
  );
  return handleResponse(res);
}
