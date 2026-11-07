import { IconTypes } from "solid-icons"
import { TiDeleteOutline } from "solid-icons/ti"
import { CgRename, CgShare } from "solid-icons/cg"
import { TbArchive, TbFileArrowRight } from "solid-icons/tb"
import { TbCopy } from "solid-icons/tb"
import { AiTwotoneDelete } from "solid-icons/ai"
import { CgFileAdd, CgFolderAdd, CgFolderRemove } from "solid-icons/cg"
import { AiOutlineCloudDownload } from "solid-icons/ai"
import { ImMoveUp } from "solid-icons/im"
import { FaSolidMagnet, FaSolidSeedling } from "solid-icons/fa"

export interface Operations {
  [key: string]: {
    icon: IconTypes
    color?: string
    p?: boolean
  }
}
export const operations: Operations = {
  rename: { icon: CgRename, color: "$accent9" },
  copy: { icon: TbCopy, color: "$success9" },
  move: { icon: TbFileArrowRight, color: "$warning9" },
  delete: { icon: AiTwotoneDelete, color: "$danger9" },
  decompress: { icon: TbArchive, color: "$neutral9" },
  mkdir: { icon: CgFolderAdd, p: true },
  recursive_move: { icon: ImMoveUp, p: true },
  remove_empty_directory: { icon: CgFolderRemove, p: true },
  new_file: { icon: CgFileAdd, p: true },
  cancel_select: { icon: TiDeleteOutline },
  download: { icon: AiOutlineCloudDownload, color: "$primary9" },
  direct_download: { icon: AiOutlineCloudDownload, color: "$primary9" },
  package_download: { icon: AiOutlineCloudDownload, color: "$primary9" },
  share: { icon: CgShare, color: "$primary9" },
  offline_download_torrent: { icon: FaSolidMagnet, color: "$accent9" },
}
// interface Operation {
//   label: string;
//   icon: IconTypes;
//   color?: string;
// }
// const operations: Operation[] = [
//   { label: "rename", icon: CgRename },
//   { label: "move", icon: OcFilemoved2 },
//   { label: "copy", icon: TbCopy },
//   { label: "delete", icon: AiOutlineDelete },
//   { label: "copy_url", icon: FiLink },
//   { label: "new_file", icon: CgFileAdd },
//   { label: "mkdir", icon: CgFolderAdd },
// ];
