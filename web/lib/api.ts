export const API_BASE = "http://localhost:8080";
const API_KEY = "test-api-key-12345";

const headers = () => ({
  "Content-Type": "application/json",
  Authorization: `Bearer ${API_KEY}`,
});

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(`HTTP ${res.status}: ${text}`);
  }
  const json = await res.json();
  return (json.data ?? json) as T;
}

// ---- Types ----

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
  signatures: Signature[];
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
  status: string;
};

// ---- API calls ----

export async function registerDocument(
  s3Key: string,
  title: string
): Promise<{ id: string; status: string }> {
  const res = await fetch(`${API_BASE}/api/v1/documents`, {
    method: "POST",
    headers: headers(),
    body: JSON.stringify({ s3_key: s3Key, title }),
  });
  return handleResponse(res);
}

export async function getDocument(id: string): Promise<Document> {
  const res = await fetch(`${API_BASE}/api/v1/documents/${id}`, {
    headers: headers(),
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
    headers: headers(),
    body: JSON.stringify({ cms, role }),
  });
  return handleResponse(res);
}

// Demo upload: POST multipart form to /api/demo/upload
// Backend stores the PDF in S3 and returns {document_id, status}.
export async function demoUpload(
  file: File,
  title: string
): Promise<DemoUploadResult> {
  const form = new FormData();
  form.append("file", file);
  form.append("title", title);
  const res = await fetch(`${API_BASE}/api/demo/upload`, {
    method: "POST",
    body: form,
  });
  return handleResponse(res);
}

// Demo sign: backend signs with the test NCA key, returns SignResult.
export async function demoSign(
  documentId: string,
  role: string
): Promise<SignResult> {
  const res = await fetch(
    `${API_BASE}/api/demo/sign/${documentId}?role=${encodeURIComponent(role)}`,
    {
      method: "POST",
      headers: headers(),
    }
  );
  return handleResponse(res);
}
