import bencode from "bencode"
import crypto from "crypto-js"

const decoder = new TextDecoder("utf-8")
const text = (value) =>
  value instanceof Uint8Array ? decoder.decode(value) : ""

// Parse ordinary BitTorrent files in the browser without uploading their contents.
export function parseTorrent(bytes) {
  const data = bencode.decode(bytes)
  const info = data?.info
  if (!info || typeof info !== "object" || Array.isArray(info)) {
    throw new Error("Invalid torrent: missing info")
  }
  const name = text(info["name.utf-8"] || info.name)
  if (!name) throw new Error("Invalid torrent: missing name")
  const files = Array.isArray(info.files)
    ? info.files.map((file) => ({
        path: (file["path.utf-8"] || file.path || []).map(text).join("/"),
        size: Number(file.length) || 0,
      }))
    : [{ path: name, size: Number(info.length) || 0 }]
  const encodedInfo = bencode.encode(info)
  return {
    name,
    files,
    totalSize: files.reduce((sum, file) => sum + file.size, 0),
    pieceSize: Number(info["piece length"]) || 0,
    pieceCount: Math.floor((info.pieces?.byteLength || 0) / 20),
    infoHash: crypto.SHA1(crypto.lib.WordArray.create(encodedInfo)).toString(),
  }
}
