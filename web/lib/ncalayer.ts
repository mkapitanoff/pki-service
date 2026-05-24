import { NCALayerClient } from "ncalayer-js-client";

let _client: NCALayerClient | null = null;

function getClient(): NCALayerClient {
  if (!_client) _client = new NCALayerClient();
  return _client;
}

export async function connectNCALayer(): Promise<void> {
  await getClient().connect();
}

export function disconnectNCALayer(): void {
  _client = null;
}

// signWithNCALayer подписывает один документ (для sign-modal).
export async function signWithNCALayer(documentBase64: string): Promise<string> {
  const client = getClient();
  const result = await client.basicsSignCMS(
    NCALayerClient.basicsStorageAll,
    documentBase64,
    NCALayerClient.basicsCMSParamsAttached,
    NCALayerClient.basicsSignerSignAny,
  );
  if (typeof result === "string") return result;
  if (Array.isArray(result) && result.length > 0) return result[0] as string;
  throw new Error("NCALayer: пустой ответ");
}

// signMultiple подписывает массив документов одной операцией (или по одному).
export async function signMultiple(documentsBase64: string[]): Promise<string[]> {
  const client = new NCALayerClient();
  await client.connect();

  if (client.multisignAvailable && documentsBase64.length > 1) {
    const result = await client.basicsSignCMS(
      NCALayerClient.basicsStorageAll,
      documentsBase64,
      NCALayerClient.basicsCMSParamsAttached,
      NCALayerClient.basicsSignerSignAny,
    );
    if (Array.isArray(result)) return result as string[];
    if (typeof result === "string") return [result];
    throw new Error("NCALayer: неожиданный ответ мультиподписания");
  }

  // Fallback — подписываем по одному
  const signatures: string[] = [];
  for (const doc of documentsBase64) {
    const sig = await client.basicsSignCMS(
      NCALayerClient.basicsStorageAll,
      doc,
      NCALayerClient.basicsCMSParamsAttached,
      NCALayerClient.basicsSignerSignAny,
    );
    if (typeof sig === "string") signatures.push(sig);
    else if (Array.isArray(sig) && sig.length > 0) signatures.push(sig[0] as string);
    else throw new Error("NCALayer: пустой ответ при подписании");
  }
  return signatures;
}
