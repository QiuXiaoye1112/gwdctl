export interface TorrentPreviewData {
  name: string
  files: Array<{ path: string; size: number }>
  totalSize: number
  pieceSize: number
  pieceCount: number
  infoHash: string
}
export function parseTorrent(bytes: Uint8Array): TorrentPreviewData
