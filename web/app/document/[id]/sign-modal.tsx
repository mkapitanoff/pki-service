"use client";

import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Loader2, Wifi, PenLine, X, CheckCircle2, AlertCircle } from "lucide-react";
import clsx from "clsx";
import { connectNCALayer, signWithNCALayer, disconnectNCALayer } from "@/lib/ncalayer";
import { signDocument } from "@/lib/api";

type Step =
  | "idle"
  | "connecting"
  | "ready"
  | "waiting_sign"
  | "verifying"
  | "generating_pdf"
  | "done"
  | "error";

const STEP_LABEL: Record<Step, string> = {
  idle: "",
  connecting: "Подключение к NCALayer...",
  ready: "NCALayer подключён. Нажмите «Подписать ЭЦП»",
  waiting_sign: "Ожидание подписи в NCALayer...",
  verifying: "Верификация подписи...",
  generating_pdf: "Формирование PDF...",
  done: "Документ успешно подписан",
  error: "",
};

type Props = {
  documentId: string;
  documentTitle: string;
  sha256Hash: string;
  role: string;
  documentBase64: string;
  onSigned: () => void;
};

export function SignModal({
  documentId,
  documentTitle,
  sha256Hash,
  role,
  documentBase64,
  onSigned,
}: Props) {
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState<Step>("idle");
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setStep("idle");
    setError(null);
    disconnectNCALayer();
  };

  const handleOpenChange = (v: boolean) => {
    if (!v) reset();
    setOpen(v);
  };

  const connect = async () => {
    setStep("connecting");
    setError(null);
    try {
      await connectNCALayer();
      setStep("ready");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Ошибка подключения к NCALayer");
      setStep("error");
    }
  };

  const sign = async () => {
    setStep("waiting_sign");
    setError(null);
    try {
      const cms = await signWithNCALayer(documentBase64);

      setStep("verifying");
      const result = await signDocument(documentId, cms, role);

      setStep("generating_pdf");
      // Brief pause so user sees the "Формирование PDF..." state
      await new Promise((r) => setTimeout(r, 600));

      setStep("done");
      setTimeout(() => {
        setOpen(false);
        reset();
        onSigned();
      }, 1200);

      void result;
    } catch (e) {
      setError(e instanceof Error ? e.message : "Ошибка подписания");
      setStep("error");
    }
  };

  const busy =
    step === "connecting" ||
    step === "waiting_sign" ||
    step === "verifying" ||
    step === "generating_pdf";

  return (
    <Dialog.Root open={open} onOpenChange={handleOpenChange}>
      <Dialog.Trigger asChild>
        <button
          type="button"
          className="flex items-center gap-2 px-4 py-2 rounded-lg bg-[#0070f3] text-white text-sm font-medium hover:bg-blue-700 transition-colors"
        >
          <PenLine className="w-4 h-4" />
          Подписать через NCALayer
        </button>
      </Dialog.Trigger>

      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/40 backdrop-blur-sm z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-md bg-white rounded-2xl shadow-xl p-6 focus:outline-none">
          <div className="flex items-start justify-between mb-4">
            <Dialog.Title className="text-lg font-semibold text-zinc-900">
              Подписание документа
            </Dialog.Title>
            <Dialog.Close asChild>
              <button
                type="button"
                disabled={busy}
                className="text-zinc-400 hover:text-zinc-600 disabled:opacity-40"
                aria-label="Закрыть"
              >
                <X className="w-5 h-5" />
              </button>
            </Dialog.Close>
          </div>

          {/* Document info */}
          <div className="bg-zinc-50 rounded-lg p-3 mb-5 space-y-1">
            <p className="text-sm font-medium text-zinc-800 truncate">
              {documentTitle}
            </p>
            <p className="text-xs text-zinc-400 font-mono break-all">
              SHA-256: {sha256Hash.slice(0, 16)}…{sha256Hash.slice(-8)}
            </p>
          </div>

          {/* Status */}
          {(step !== "idle" || error) && (
            <div
              className={clsx(
                "flex items-center gap-2 rounded-lg px-3 py-2 mb-4 text-sm",
                step === "done"
                  ? "bg-green-50 text-[#00b894]"
                  : step === "error"
                    ? "bg-red-50 text-[#d63031]"
                    : "bg-blue-50 text-[#0070f3]"
              )}
            >
              {busy && <Loader2 className="w-4 h-4 animate-spin shrink-0" />}
              {step === "done" && (
                <CheckCircle2 className="w-4 h-4 shrink-0" />
              )}
              {step === "error" && (
                <AlertCircle className="w-4 h-4 shrink-0" />
              )}
              <span>{step === "error" ? error : STEP_LABEL[step]}</span>
            </div>
          )}

          {/* Actions */}
          <div className="flex flex-col gap-3">
            {(step === "idle" || step === "error") && (
              <button
                type="button"
                onClick={connect}
                className="w-full py-2.5 rounded-xl font-medium text-sm bg-[#0070f3] text-white hover:bg-blue-700 transition-colors flex items-center justify-center gap-2"
              >
                <Wifi className="w-4 h-4" />
                Подключиться к NCALayer
              </button>
            )}
            {step === "ready" && (
              <button
                type="button"
                onClick={sign}
                className="w-full py-2.5 rounded-xl font-medium text-sm bg-[#00b894] text-white hover:bg-green-600 transition-colors flex items-center justify-center gap-2"
              >
                <PenLine className="w-4 h-4" />
                Подписать ЭЦП
              </button>
            )}
            {busy && (
              <div className="w-full py-2.5 rounded-xl bg-zinc-100 text-zinc-400 text-sm text-center">
                Пожалуйста, подождите...
              </div>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
