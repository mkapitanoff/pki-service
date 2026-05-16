type PendingRequest = {
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
};

class NCALayerWsClient {
  private ws: WebSocket | null = null;
  // FIFO queue — NCALayer processes one request at a time; each response
  // belongs to the oldest outstanding request.
  private queue: PendingRequest[] = [];

  connect(): Promise<void> {
    return new Promise((resolve, reject) => {
      if (this.ws?.readyState === WebSocket.OPEN) { resolve(); return; }
      const ws = new WebSocket("wss://127.0.0.1:13579/");
      const timer = setTimeout(() => { ws.close(); reject(new Error("NCALayer: таймаут подключения")); }, 5000);

      ws.onerror = () => { clearTimeout(timer); reject(new Error("NCALayer: не удалось подключиться")); };
      ws.onclose = () => { this.ws = null; };

      // NCALayer 1.4 sends {"result":{"version":"1.4"}} as the very first message
      // when the socket opens — before any request is sent.
      ws.onmessage = (e) => {
        clearTimeout(timer);
        console.log("NCALayer recv (version):", e.data);
        try {
          const msg = JSON.parse(e.data as string) as { result?: { version?: string } };
          if (msg.result?.version) {
            this.ws = ws;
            ws.onmessage = (ev) => this.handleMessage(ev);
            resolve();
            return;
          }
        } catch { /* ignore */ }
        ws.close();
        reject(new Error("NCALayer: неожиданное первое сообщение: " + e.data));
      };
    });
  }

  private handleMessage(e: MessageEvent): void {
    console.log("NCALayer recv:", e.data);
    const handler = this.queue.shift();
    if (!handler) return;
    try {
      const msg = JSON.parse(e.data as string) as Record<string, unknown>;
      // commonUtils response: {"code":"200","responseObject":"<cms>"}
      if ("code" in msg) {
        if (msg.code !== "200") {
          handler.reject(new Error(`NCALayer ${msg.code}: ${msg.message ?? ""}`));
        } else {
          handler.resolve(msg.responseObject);
        }
        return;
      }
      // basics module response: {"status":true,"body":{"result":"<cms>"}}
      if ("status" in msg) {
        const body = msg.body as Record<string, unknown> | undefined;
        if (!msg.status || !body?.result) {
          handler.reject(new Error((msg.message as string) ?? "NCALayer: отменено пользователем"));
        } else {
          handler.resolve(body.result);
        }
        return;
      }
      handler.reject(new Error("NCALayer: неожиданный ответ: " + e.data));
    } catch (err) {
      handler.reject(err instanceof Error ? err : new Error(String(err)));
    }
  }

  // call sends a request and enqueues a handler for its response.
  // args must be either a positional array (commonUtils) or an object (basics module).
  call(module: string, method: string, args: unknown): Promise<unknown> {
    return new Promise((resolve, reject) => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
        reject(new Error("NCALayer не подключён")); return;
      }
      this.queue.push({ resolve, reject });
      const msg = { module, method, args };
      console.log("NCALayer send:", JSON.stringify(msg).slice(0, 300));
      this.ws.send(JSON.stringify(msg));
    });
  }

  disconnect(): void {
    this.ws?.close();
    this.ws = null;
    this.queue = [];
  }
}

const client = new NCALayerWsClient();

export async function connectNCALayer(): Promise<void> {
  await client.connect();
}

export async function signWithNCALayer(documentBase64: string): Promise<string> {
  // createCAdESFromBase64 args are positional: [storageType, keyType, data, attach]
  // Try PKCS12 (file key) first, fall back to AKI (hardware token).
  for (const storage of ["PKCS12", "AKI"]) {
    try {
      const result = await client.call(
        "kz.gov.pki.knca.commonUtils",
        "createCAdESFromBase64",
        [storage, "SIGNATURE", documentBase64, true],
      );
      console.log("createCAdESFromBase64 result type:", typeof result);
      if (typeof result === "string" && result.length > 0) return result;
      throw new Error("NCALayer: пустой ответ от " + storage);
    } catch (e) {
      if (storage === "AKI") throw e; // both storages failed
      console.warn(`NCALayer ${storage} failed, trying AKI:`, e);
    }
  }
  throw new Error("NCALayer: не удалось подписать");
}

export function disconnectNCALayer(): void {
  client.disconnect();
}
