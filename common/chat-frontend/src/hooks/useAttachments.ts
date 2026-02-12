import { useCallback, useRef, useState } from "react";
import { getAccessToken } from "@/lib/auth";

const EXTENSION_TYPES: Record<string, string> = {
  // Images
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  svg: "image/svg+xml",
  bmp: "image/bmp",
  ico: "image/x-icon",
  // Audio
  mp3: "audio/mpeg",
  wav: "audio/wav",
  ogg: "audio/ogg",
  flac: "audio/flac",
  aac: "audio/aac",
  m4a: "audio/mp4",
  wma: "audio/x-ms-wma",
  // Video
  mp4: "video/mp4",
  webm: "video/webm",
  mkv: "video/x-matroska",
  avi: "video/x-msvideo",
  mov: "video/quicktime",
  // Documents
  pdf: "application/pdf",
  doc: "application/msword",
  docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  xls: "application/vnd.ms-excel",
  xlsx: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  ppt: "application/vnd.ms-powerpoint",
  pptx: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  csv: "text/csv",
  txt: "text/plain",
  md: "text/markdown",
  json: "application/json",
  xml: "application/xml",
  // Archives
  zip: "application/zip",
  gz: "application/gzip",
  tar: "application/x-tar",
};

function guessContentType(file: File): string {
  if (file.type) return file.type;
  const ext = file.name.split(".").pop()?.toLowerCase();
  return (ext && EXTENSION_TYPES[ext]) || "application/octet-stream";
}

export type PendingAttachment = {
  localId: string;
  file?: File;
  name: string;
  contentType: string;
  progress: number;
  status: "uploading" | "uploaded" | "error";
  attachmentId?: string;
  error?: string;
  /** True for attachments pre-loaded from an existing entry (not freshly uploaded). */
  isExistingReference?: boolean;
};

type InternalAttachment = PendingAttachment & {
  xhr?: XMLHttpRequest;
};

let nextLocalId = 0;

export function useAttachments() {
  const [attachments, setAttachments] = useState<PendingAttachment[]>([]);
  // Use a ref for the internal state so XHR callbacks always see the latest list
  const internalsRef = useRef<InternalAttachment[]>([]);

  const syncState = useCallback(() => {
    // Strip internal xhr field before exposing to consumers
    setAttachments(
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      internalsRef.current.map(({ xhr, ...rest }) => rest),
    );
  }, []);

  const addFiles = useCallback(
    (files: FileList | File[]) => {
      const fileArray = Array.from(files);

      for (const file of fileArray) {
        const localId = `att-${++nextLocalId}-${Date.now()}`;
        const entry: InternalAttachment = {
          localId,
          file,
          name: file.name,
          contentType: guessContentType(file),
          progress: 0,
          status: "uploading",
        };

        internalsRef.current = [...internalsRef.current, entry];
        syncState();

        // Start upload using XMLHttpRequest for progress tracking
        const xhr = new XMLHttpRequest();
        entry.xhr = xhr;

        xhr.upload.onprogress = (event) => {
          if (event.lengthComputable) {
            const progress = Math.round((event.loaded / event.total) * 100);
            internalsRef.current = internalsRef.current.map((a) => (a.localId === localId ? { ...a, progress } : a));
            syncState();
          }
        };

        xhr.onload = () => {
          if (xhr.status >= 200 && xhr.status < 300) {
            try {
              const response = JSON.parse(xhr.responseText);
              internalsRef.current = internalsRef.current.map((a) =>
                a.localId === localId
                  ? {
                      ...a,
                      status: "uploaded" as const,
                      progress: 100,
                      attachmentId: response.id,
                      xhr: undefined,
                    }
                  : a,
              );
            } catch {
              internalsRef.current = internalsRef.current.map((a) =>
                a.localId === localId
                  ? {
                      ...a,
                      status: "error" as const,
                      error: "Invalid server response",
                      xhr: undefined,
                    }
                  : a,
              );
            }
          } else {
            internalsRef.current = internalsRef.current.map((a) =>
              a.localId === localId
                ? {
                    ...a,
                    status: "error" as const,
                    error: `Upload failed (${xhr.status})`,
                    xhr: undefined,
                  }
                : a,
            );
          }
          syncState();
        };

        xhr.onerror = () => {
          internalsRef.current = internalsRef.current.map((a) =>
            a.localId === localId
              ? {
                  ...a,
                  status: "error" as const,
                  error: "Network error",
                  xhr: undefined,
                }
              : a,
          );
          syncState();
        };

        xhr.onabort = () => {
          internalsRef.current = internalsRef.current.filter((a) => a.localId !== localId);
          syncState();
        };

        const formData = new FormData();
        formData.append("file", file);

        xhr.open("POST", "/v1/attachments");
        const token = getAccessToken();
        if (token) {
          xhr.setRequestHeader("Authorization", `Bearer ${token}`);
        }
        xhr.send(formData);
      }
    },
    [syncState],
  );

  /**
   * Pre-populate the attachment list with existing attachments from a message.
   * Used when editing/forking a message that already has attachments.
   * These are marked as "uploaded" with their existing attachment ID so they
   * can be sent as references without re-uploading.
   */
  const preloadExisting = useCallback(
    (existing: Array<{ attachmentId: string; contentType: string; name?: string }>) => {
      for (const att of existing) {
        const localId = `att-${++nextLocalId}-${Date.now()}`;
        const entry: InternalAttachment = {
          localId,
          name: att.name ?? "attachment",
          contentType: att.contentType,
          progress: 100,
          status: "uploaded",
          attachmentId: att.attachmentId,
          isExistingReference: true,
        };
        internalsRef.current = [...internalsRef.current, entry];
      }
      syncState();
    },
    [syncState],
  );

  const removeAttachment = useCallback(
    (localId: string) => {
      const entry = internalsRef.current.find((a) => a.localId === localId);
      if (!entry) return;

      if (entry.status === "uploading" && entry.xhr) {
        // Abort will trigger onabort which removes from state
        entry.xhr.abort();
        return;
      }

      // Only delete from server if it was freshly uploaded (not an existing reference)
      if (entry.status === "uploaded" && entry.attachmentId && !entry.isExistingReference) {
        // Fire-and-forget DELETE to clean up server-side
        const token = getAccessToken();
        fetch(`/v1/attachments/${entry.attachmentId}`, {
          method: "DELETE",
          headers: token ? { Authorization: `Bearer ${token}` } : {},
        }).catch(() => {
          // Ignore delete failures - attachment will expire
        });
      }

      internalsRef.current = internalsRef.current.filter((a) => a.localId !== localId);
      syncState();
    },
    [syncState],
  );

  const clearAll = useCallback(() => {
    // Abort any in-progress uploads
    for (const entry of internalsRef.current) {
      if (entry.status === "uploading" && entry.xhr) {
        entry.xhr.abort();
      }
      // Clean up uploaded but unsent attachments (not existing references)
      if (entry.status === "uploaded" && entry.attachmentId && !entry.isExistingReference) {
        const token = getAccessToken();
        fetch(`/v1/attachments/${entry.attachmentId}`, {
          method: "DELETE",
          headers: token ? { Authorization: `Bearer ${token}` } : {},
        }).catch(() => {});
      }
    }
    internalsRef.current = [];
    syncState();
  }, [syncState]);

  const getUploadedIds = useCallback((): string[] => {
    return internalsRef.current.filter((a) => a.status === "uploaded" && a.attachmentId).map((a) => a.attachmentId!);
  }, []);

  /** Reset state after send — does NOT delete uploaded attachments from the server. */
  const resetAfterSend = useCallback(() => {
    // Abort any still-in-progress uploads (they weren't submitted)
    for (const entry of internalsRef.current) {
      if (entry.status === "uploading" && entry.xhr) {
        entry.xhr.abort();
      }
    }
    internalsRef.current = [];
    syncState();
  }, [syncState]);

  return {
    attachments,
    addFiles,
    preloadExisting,
    removeAttachment,
    clearAll,
    getUploadedIds,
    resetAfterSend,
  };
}
