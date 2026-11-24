import { createSignal } from "solid-js"
import { UploadOverwrite } from "./types"

export interface UploadConflictRequest {
  baseDir: string
  files: File[]
  conflicts: string[]
  directFileConflicts: Set<string>
  onConfirm: (
    toUpload: File[],
    baseDir: string,
    overwrite: UploadOverwrite,
  ) => void | Promise<void>
}

export const [conflictOpen, setConflictOpen] = createSignal(false)
export const [pendingConflict, setPendingConflict] =
  createSignal<UploadConflictRequest | null>(null)
