declare module "ncalayer-js-client" {
  export class NCALayerClient {
    static basicsStorageAll: string;
    static basicsStoragePKCS12: string;
    static basicsCMSParamsAttached: string;
    static basicsCMSParamsDetached: string;
    static basicsSignerSignAny: string;
    static basicsSignerAny: string;

    connect(): Promise<void>;
    get multisignAvailable(): boolean;

    basicsSignCMS(
      storage: string,
      data: string | string[],
      params: string,
      signer: string,
    ): Promise<string | string[]>;

    createCAdESFromBase64(
      storage: string,
      keyType: string,
      data: string,
      attach: boolean,
    ): Promise<string>;
  }
}
