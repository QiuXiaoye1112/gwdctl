import mitt from "mitt"
import { SeedInfo, TorrentInfo } from "~/types"

type Events = {
  to: string
  gallery: string
  tool: string
  pathname: string
  extract: string
  torrent_parsed: { torrentData: string; info: TorrentInfo }
  seed_parsed: { seedData: string; fileName: string; info: SeedInfo }
  generate_transfer_seed: { paths: string[] }
  track_tasks: { type: "copy" | "move"; ids: string[] }
  "plugin:file_action_registered": any
  "plugin:header_action_registered": any
}

export const bus = mitt<Events>()
