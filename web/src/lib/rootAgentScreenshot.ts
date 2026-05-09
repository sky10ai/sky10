import type { DebugScreenshotParams } from "./rpc";
import type {
  RootAgentPageContext,
  RootAgentScreenshotContext,
} from "./rootAgentContext";

export interface CapturedScreenshot extends RootAgentScreenshotContext {
  blob: Blob;
  contentType: string;
  url: string;
}

export function safeTimestamp() {
  return new Date().toISOString().replace(/[:.]/g, "-");
}

export function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

export function downloadText(text: string, filename: string, contentType: string) {
  downloadBlob(new Blob([text], { type: contentType }), filename);
}

function canvasToBlob(canvas: HTMLCanvasElement) {
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) {
        resolve(blob);
        return;
      }
      reject(new Error("Screenshot capture failed."));
    }, "image/png");
  });
}

function bytesToBase64(bytes: Uint8Array) {
  let binary = "";
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    const chunk = bytes.subarray(offset, offset + chunkSize);
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}

export async function captureBrowserScreenshot(): Promise<CapturedScreenshot> {
  const getDisplayMedia = navigator.mediaDevices?.getDisplayMedia?.bind(
    navigator.mediaDevices,
  );
  if (!getDisplayMedia) {
    throw new Error("Screen capture is not available in this browser.");
  }

  const stream = await getDisplayMedia({ audio: false, video: true });
  try {
    const video = document.createElement("video");
    const ready = new Promise<void>((resolve, reject) => {
      video.onloadedmetadata = () => resolve();
      video.onerror = () => reject(new Error("Could not read the captured screen."));
    });
    video.muted = true;
    video.playsInline = true;
    video.srcObject = stream;
    await ready;
    await video.play();
    await new Promise<void>((resolve) => window.requestAnimationFrame(() => resolve()));

    if (video.videoWidth <= 0 || video.videoHeight <= 0) {
      throw new Error("The selected screen did not produce an image.");
    }

    const canvas = document.createElement("canvas");
    canvas.width = video.videoWidth;
    canvas.height = video.videoHeight;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Canvas is not available.");
    context.drawImage(video, 0, 0, canvas.width, canvas.height);

    const blob = await canvasToBlob(canvas);
    const capturedAt = new Date().toISOString();
    const filename = `sky10-context-${safeTimestamp()}.png`;

    return {
      blob,
      capturedAt,
      contentType: blob.type || "image/png",
      filename,
      height: canvas.height,
      sizeBytes: blob.size,
      url: URL.createObjectURL(blob),
      width: canvas.width,
    };
  } finally {
    stream.getTracks().forEach((track) => track.stop());
  }
}

export async function buildDebugScreenshotUpload(
  screenshot: CapturedScreenshot,
  pageContext: RootAgentPageContext,
): Promise<DebugScreenshotParams> {
  const bytes = new Uint8Array(await screenshot.blob.arrayBuffer());
  return {
    captured_at: screenshot.capturedAt,
    content_type: screenshot.contentType || screenshot.blob.type || "image/png",
    data_base64: bytesToBase64(bytes),
    filename: screenshot.filename,
    height: screenshot.height,
    page_context: pageContext,
    size_bytes: screenshot.sizeBytes,
    width: screenshot.width,
  };
}
