import { SeedFormat, SeedHashMatrix } from "~/types"

export interface UploadSeedOptions {
  formats: SeedFormat[]
  hash_matrix: SeedHashMatrix
  piece_size: number
}

export const seedUploadHeaders = (
  options?: UploadSeedOptions,
): Record<string, string> => {
  if (!options?.formats.length) return {}
  return {
    "X-Seed-Sidecars": options.formats.join(","),
    "X-Seed-Hash-Matrix": encodeURIComponent(
      JSON.stringify(options.hash_matrix),
    ),
    "X-Seed-Piece-Size": String(options.piece_size),
  }
}

type Status =
  "pending" | "hashing" | "uploading" | "backending" | "success" | "error"
export interface UploadFileProps {
  name: string
  path: string
  fullPath?: string
  batchId?: string
  batchLabel?: string
  batchTotal?: number
  size: number
  progress: number
  speed: number
  status: Status
  msg?: string
}
export const StatusBadge = {
  pending: "neutral",
  hashing: "warning",
  uploading: "info",
  backending: "info",
  success: "success",
  error: "danger",
} as const
export type UploadOverwrite = boolean | "skip"
export type SetUpload = (key: keyof UploadFileProps, value: any) => void
export type Upload = (
  uploadPath: string,
  file: File,
  setUpload: SetUpload,
  overwrite: UploadOverwrite,
  rapid: boolean,
  seedOptions?: UploadSeedOptions,
  signal?: AbortSignal,
) => Promise<Error | undefined>
