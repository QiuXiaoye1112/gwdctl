import { test } from "node:test"
import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import bencode from "bencode"
import { parseTorrent } from "./torrent_parse.js"

test("reads torrent files and the info hash without altering data", () => {
  const info = {
    name: "demo",
    "piece length": 16384,
    pieces: new Uint8Array(40),
    files: [
      { length: 42, path: ["folder", "file.txt"] },
      { length: 8, path: ["other.txt"] },
    ],
  }
  const data = bencode.encode({ info })
  const result = parseTorrent(data)
  assert.equal(result.name, "demo")
  assert.equal(result.totalSize, 50)
  assert.equal(result.pieceCount, 2)
  assert.equal(result.files[0].path, "folder/file.txt")
  assert.equal(
    result.infoHash,
    createHash("sha1").update(bencode.encode(info)).digest("hex"),
  )
})

test("rejects incomplete torrent files", () => {
  assert.throws(
    () => parseTorrent(bencode.encode({ announce: "example" })),
    /missing info/,
  )
})
