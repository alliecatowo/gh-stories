import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { VISIBILITY_LABELS, type Visibility } from "@gh-stories/contracts";
import type { AudienceOption, ComposerDraft } from "./types.js";
import "./StoryComposer.css";

export interface StoryComposerDefaults {
  visibility: Visibility;
  audienceListId?: string;
  allowReplies: boolean;
  allowReactions: boolean;
}

export interface StoryComposerProps {
  onSubmit: (draft: ComposerDraft) => Promise<void>;
  audiences: AudienceOption[];
  defaults: StoryComposerDefaults;
  onCancel: () => void;
  container?: Element | DocumentFragment;
}

const ACCEPT_MIMES = [
  "image/jpeg",
  "image/png",
  "image/webp",
  "image/gif",
  "video/mp4",
  "video/webm",
  "video/quicktime",
];
const MAX_BYTES = 100 * 1024 * 1024;
type Status =
  | "idle"
  | "validating"
  | "validation-error"
  | "offline"
  | "preview"
  | "uploading"
  | "processing"
  | "published"
  | "failed";

type Aspect = "original" | "square" | "portrait";
interface Overlay {
  text: string;
  x: number;
  y: number;
  size: "sm" | "md" | "lg";
  color: "white" | "black";
}

function loadImage(src: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error("image load failed"));
    img.src = src;
  });
}

/** Draws rotation, a centered fixed-aspect crop, and an optional text
 * overlay onto a canvas, producing the exported Blob. Two-pass (rotate,
 * then crop) so the rotation math stays simple. */
async function exportImage(
  objectUrl: string,
  rotationDeg: number,
  aspect: Aspect,
  overlay: Overlay | null,
  originalMime: string,
): Promise<Blob> {
  const img = await loadImage(objectUrl);
  const swapped = rotationDeg % 180 !== 0;
  const canvasW = swapped ? img.naturalHeight : img.naturalWidth;
  const canvasH = swapped ? img.naturalWidth : img.naturalHeight;

  const rotated = document.createElement("canvas");
  rotated.width = canvasW;
  rotated.height = canvasH;
  const rctx = rotated.getContext("2d");
  if (!rctx) throw new Error("canvas unsupported");
  rctx.translate(canvasW / 2, canvasH / 2);
  rctx.rotate((rotationDeg * Math.PI) / 180);
  rctx.drawImage(img, -img.naturalWidth / 2, -img.naturalHeight / 2);

  const targetAspect = aspect === "square" ? 1 : aspect === "portrait" ? 4 / 5 : canvasW / canvasH;
  let cropW = canvasW;
  let cropH = canvasH;
  if (canvasW / canvasH > targetAspect) cropW = canvasH * targetAspect;
  else cropH = canvasW / targetAspect;
  const cropX = (canvasW - cropW) / 2;
  const cropY = (canvasH - cropH) / 2;

  const final = document.createElement("canvas");
  final.width = Math.round(cropW);
  final.height = Math.round(cropH);
  const fctx = final.getContext("2d");
  if (!fctx) throw new Error("canvas unsupported");
  fctx.drawImage(rotated, cropX, cropY, cropW, cropH, 0, 0, final.width, final.height);

  if (overlay && overlay.text.trim() !== "") {
    const fontPx = overlay.size === "lg" ? 48 : overlay.size === "md" ? 32 : 20;
    fctx.font = `700 ${fontPx}px sans-serif`;
    fctx.fillStyle = overlay.color;
    fctx.textAlign = "center";
    fctx.textBaseline = "middle";
    fctx.fillText(overlay.text, overlay.x * final.width, overlay.y * final.height);
  }

  const outputMime = originalMime === "image/gif" ? "image/png" : originalMime;
  return new Promise((resolve, reject) => {
    final.toBlob(
      (blob) => (blob ? resolve(blob) : reject(new Error("export failed"))),
      outputMime,
      0.92,
    );
  });
}

/** Upload -> Preview -> Post. Secondary edits live behind compact controls. */
export function StoryComposer(props: StoryComposerProps): React.JSX.Element {
  const { onSubmit, audiences, defaults, onCancel, container } = props;

  const [status, setStatus] = useState<Status>("idle");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [file, setFile] = useState<File | null>(null);
  const [objectUrl, setObjectUrl] = useState<string | null>(null);
  const [kind, setKind] = useState<"image" | "video" | null>(null);
  const [progress, setProgress] = useState(0);
  const [dragOver, setDragOver] = useState(false);

  const [rotation, setRotation] = useState(0);
  const [aspect, setAspect] = useState<Aspect>("original");
  const [overlay, setOverlay] = useState<Overlay | null>(null);

  const [durationMs, setDurationMs] = useState(0);
  const [startMs, setStartMs] = useState(0);
  const [endMs, setEndMs] = useState(0);
  const [muted, setMuted] = useState(false);

  const [caption, setCaption] = useState("");
  const [altText, setAltText] = useState("");
  const [visibility, setVisibility] = useState<Visibility>(defaults.visibility);
  const [audienceListId, setAudienceListId] = useState<string | undefined>(defaults.audienceListId);
  const [allowReplies, setAllowReplies] = useState(defaults.allowReplies);
  const [allowReactions, setAllowReactions] = useState(defaults.allowReactions);

  const previewRef = useRef<HTMLDivElement | null>(null);
  const draggingOverlayRef = useRef(false);

  useEffect(() => {
    function goOffline() {
      setStatus((s) => (s === "validating" ? "offline" : s));
    }
    window.addEventListener("offline", goOffline);
    return () => window.removeEventListener("offline", goOffline);
  }, []);

  useEffect(() => {
    return () => {
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [objectUrl]);

  function reset() {
    setFile(null);
    if (objectUrl) URL.revokeObjectURL(objectUrl);
    setObjectUrl(null);
    setKind(null);
    setRotation(0);
    setAspect("original");
    setOverlay(null);
    setStatus("idle");
    setErrorMessage(null);
  }

  function pickFile(candidate: File | undefined) {
    if (!candidate) return;
    setStatus("validating");
    if (!navigator.onLine) {
      setStatus("offline");
      return;
    }
    if (!ACCEPT_MIMES.includes(candidate.type)) {
      setErrorMessage("Unsupported file type. Use JPEG, PNG, WebP, GIF, MP4, WebM or MOV.");
      setStatus("validation-error");
      return;
    }
    if (candidate.size > MAX_BYTES) {
      setErrorMessage("That file is larger than 100MB.");
      setStatus("validation-error");
      return;
    }
    const url = URL.createObjectURL(candidate);
    setFile(candidate);
    setObjectUrl(url);
    const nextKind = candidate.type.startsWith("video/") ? "video" : "image";
    setKind(nextKind);
    setStatus("preview");
  }

  function onOverlayPointerDown(event: React.PointerEvent<HTMLDivElement>) {
    if (!overlay) return;
    draggingOverlayRef.current = true;
    (event.target as HTMLElement).setPointerCapture(event.pointerId);
  }
  function onOverlayPointerMove(event: React.PointerEvent<HTMLDivElement>) {
    if (!draggingOverlayRef.current || !overlay || !previewRef.current) return;
    const rect = previewRef.current.getBoundingClientRect();
    const x = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width));
    const y = Math.min(1, Math.max(0, (event.clientY - rect.top) / rect.height));
    setOverlay({ ...overlay, x, y });
  }
  function onOverlayPointerUp() {
    draggingOverlayRef.current = false;
  }

  async function submit() {
    if (!file || !objectUrl) return;
    setStatus("uploading");
    setErrorMessage(null);
    setProgress(0);
    const rampTimer = window.setInterval(() => {
      setProgress((p) => (p >= 95 ? p : p + (95 - p) * 0.15));
    }, 200);
    try {
      const needsExport = kind === "image" && (rotation !== 0 || aspect !== "original" || overlay !== null);
      const exported = needsExport
        ? await exportImage(objectUrl, rotation, aspect, overlay, file.type)
        : file;
      const draft: ComposerDraft = {
        file: exported,
        filename: file.name,
        caption,
        altText,
        visibility,
        audienceListId: visibility === "custom_list" ? audienceListId : undefined,
        allowReplies,
        allowReactions,
        videoEdit: kind === "video" ? { startMs, endMs: endMs || durationMs, muted } : undefined,
      };
      await onSubmit(draft);
      window.clearInterval(rampTimer);
      setProgress(100);
      setStatus("processing");
      window.setTimeout(() => setStatus("published"), 1200);
    } catch {
      window.clearInterval(rampTimer);
      setErrorMessage("Something went wrong while posting. You can try again.");
      setStatus("failed");
    }
  }

  const selectedAudience = audiences.find(
    (option) => option.visibility === visibility && (visibility !== "custom_list" || option.audienceListId === audienceListId),
  );

  const content = (
    <div className="ghs-root ghs-composer">
      {status === "idle" || status === "validating" || status === "validation-error" || status === "offline" ? (
        <div
          className="ghs-composer__drop"
          data-over={dragOver || undefined}
          onDragOver={(event) => {
            event.preventDefault();
            setDragOver(true);
          }}
          onDragLeave={() => setDragOver(false)}
          onDrop={(event) => {
            event.preventDefault();
            setDragOver(false);
            pickFile(event.dataTransfer.files[0]);
          }}
        >
          <p>Drag a photo or video here, or choose a file.</p>
          <label className="ghs-composer__pick">
            Choose file
            <input
              type="file"
              accept={ACCEPT_MIMES.join(",")}
              onChange={(event) => pickFile(event.target.files?.[0])}
            />
          </label>
          {status === "validation-error" && errorMessage ? (
            <p className="ghs-composer__error" role="alert">
              {errorMessage}
            </p>
          ) : null}
          {status === "offline" ? (
            <p className="ghs-composer__error" role="alert">
              You're offline. Check your connection and try again.
            </p>
          ) : null}
          <button type="button" className="ghs-composer__cancel" onClick={onCancel}>
            Cancel
          </button>
        </div>
      ) : null}

      {status === "preview" && file && objectUrl && kind ? (
        <div className="ghs-composer__editor">
          <div
            className="ghs-composer__preview"
            ref={previewRef}
            onPointerMove={onOverlayPointerMove}
            onPointerUp={onOverlayPointerUp}
          >
            {kind === "image" ? (
              <img
                src={objectUrl}
                alt=""
                style={{ transform: `rotate(${rotation}deg)` }}
                className="ghs-composer__media"
              />
            ) : (
              <video
                src={objectUrl}
                className="ghs-composer__media"
                controls
                muted={muted}
                onLoadedMetadata={(event) => {
                  const d = Math.round(event.currentTarget.duration * 1000);
                  setDurationMs(d);
                  setEndMs(d);
                }}
              />
            )}
            {overlay ? (
              <div
                className="ghs-composer__overlay-text"
                data-color={overlay.color}
                data-size={overlay.size}
                style={{ left: `${overlay.x * 100}%`, top: `${overlay.y * 100}%` }}
                onPointerDown={onOverlayPointerDown}
              >
                {overlay.text || "Your text"}
              </div>
            ) : null}
          </div>

          {kind === "image" ? (
            <div className="ghs-composer__tools">
              <button type="button" onClick={() => setRotation((r) => (r + 90) % 360)}>
                Rotate 90°
              </button>
              <div className="ghs-composer__aspects">
                {(["original", "square", "portrait"] as const).map((value) => (
                  <button
                    key={value}
                    type="button"
                    aria-pressed={aspect === value}
                    onClick={() => setAspect(value)}
                  >
                    {value}
                  </button>
                ))}
              </div>
              {overlay ? (
                <div className="ghs-composer__overlay-tools">
                  <input
                    type="text"
                    value={overlay.text}
                    placeholder="Add text"
                    onChange={(event) => setOverlay({ ...overlay, text: event.target.value })}
                  />
                  <select
                    value={overlay.size}
                    onChange={(event) => setOverlay({ ...overlay, size: event.target.value as Overlay["size"] })}
                  >
                    <option value="sm">Small</option>
                    <option value="md">Medium</option>
                    <option value="lg">Large</option>
                  </select>
                  <select
                    value={overlay.color}
                    onChange={(event) => setOverlay({ ...overlay, color: event.target.value as Overlay["color"] })}
                  >
                    <option value="white">White</option>
                    <option value="black">Black</option>
                  </select>
                  <button type="button" onClick={() => setOverlay(null)}>
                    Remove text
                  </button>
                </div>
              ) : (
                <button type="button" onClick={() => setOverlay({ text: "", x: 0.5, y: 0.5, size: "md", color: "white" })}>
                  Add text
                </button>
              )}
            </div>
          ) : (
            <div className="ghs-composer__tools">
              <label className="ghs-composer__trim">
                Start
                <input
                  type="range"
                  min={0}
                  max={durationMs}
                  value={startMs}
                  onChange={(event) => setStartMs(Math.min(Number(event.target.value), endMs))}
                />
              </label>
              <label className="ghs-composer__trim">
                End
                <input
                  type="range"
                  min={0}
                  max={durationMs}
                  value={endMs}
                  onChange={(event) => setEndMs(Math.max(Number(event.target.value), startMs))}
                />
              </label>
              <label className="ghs-composer__checkbox">
                <input type="checkbox" checked={muted} onChange={(event) => setMuted(event.target.checked)} />
                Mute
              </label>
            </div>
          )}

          <label className="ghs-composer__field">
            Caption
            <input type="text" value={caption} onChange={(event) => setCaption(event.target.value)} maxLength={280} />
          </label>
          <label className="ghs-composer__field">
            Accessibility description
            <input type="text" value={altText} onChange={(event) => setAltText(event.target.value)} maxLength={280} />
          </label>

          <label className="ghs-composer__field">
            Who can see this
            <select
              value={`${visibility}:${audienceListId ?? ""}`}
              onChange={(event) => {
                const [nextVisibility, nextListId] = event.target.value.split(":");
                setVisibility(nextVisibility as Visibility);
                setAudienceListId(nextListId || undefined);
              }}
            >
              {audiences.map((option) => (
                <option key={`${option.visibility}:${option.audienceListId ?? ""}`} value={`${option.visibility}:${option.audienceListId ?? ""}`}>
                  {option.label}
                </option>
              ))}
            </select>
          </label>
          <p className="ghs-composer__audience-description">
            {selectedAudience?.description ?? VISIBILITY_LABELS[visibility]}
          </p>

          <div className="ghs-composer__toggles">
            <label className="ghs-composer__checkbox">
              <input type="checkbox" checked={allowReplies} onChange={(event) => setAllowReplies(event.target.checked)} />
              Allow replies
            </label>
            <label className="ghs-composer__checkbox">
              <input type="checkbox" checked={allowReactions} onChange={(event) => setAllowReactions(event.target.checked)} />
              Allow reactions
            </label>
          </div>

          <div className="ghs-composer__actions">
            <button type="button" onClick={reset}>
              Back
            </button>
            <button type="button" className="ghs-composer__post" onClick={() => void submit()}>
              Post
            </button>
          </div>
        </div>
      ) : null}

      {status === "uploading" ? (
        <div className="ghs-composer__status">
          <p>Uploading… {Math.round(progress)}%</p>
          <div className="ghs-composer__progress">
            <div className="ghs-composer__progress-fill" style={{ width: `${progress}%` }} />
          </div>
          <button type="button" onClick={() => setStatus("preview")}>
            Cancel
          </button>
        </div>
      ) : null}

      {status === "processing" ? (
        <div className="ghs-composer__status">
          <p>Processing…</p>
        </div>
      ) : null}

      {status === "published" ? (
        <div className="ghs-composer__status">
          <p>Story posted.</p>
          <button type="button" onClick={onCancel}>
            Done
          </button>
        </div>
      ) : null}

      {status === "failed" ? (
        <div className="ghs-composer__status">
          <p role="alert">{errorMessage}</p>
          <button type="button" onClick={() => void submit()}>
            Retry
          </button>
          <button type="button" onClick={onCancel}>
            Cancel
          </button>
        </div>
      ) : null}
    </div>
  );

  return container ? createPortal(content, container) : content;
}
