import { createLocalStorage } from "@solid-primitives/storage"
import { isMobile } from "~/utils/compatibility"

const [local, setLocal, { remove, clear, toJSON }] = createLocalStorage()
// export function isValidKey(
//   key: string | number | symbol,
//   object: object
// ): key is keyof typeof object {
//   return key in object
// }

export const initialLocalSettings = [
  {
    key: "global_default_layout",
    default: "list",
    type: "select",
    options: ["list", "grid", "image"],
  },
  {
    key: "show_folder_in_image_view",
    default: "top",
    type: "select",
    options: ["top", "bottom", "none"],
  },
  {
    key: "show_sidebar",
    default: "none",
    type: "select",
    options: ["none", "visible"],
  },
  {
    key: "show_count_msg",
    default: "none",
    type: "select",
    options: ["none", "visible"],
  },
  {
    key: "position_of_header_navbar",
    default: "static",
    type: "select",
    options: ["static", "sticky", "only_navbar_sticky"],
  },
  {
    key: "grid_item_size",
    default: "90",
    type: "number",
  },
  {
    key: "list_item_filename_overflow",
    default: "ellipsis",
    type: "select",
    options: ["ellipsis", "scrollable", "multi_line"],
  },
  {
    key: "editor_font_size",
    default: "14",
    type: "number",
    hidden: true,
  },
  {
    key: "editor_word_wrap",
    default: "false",
    type: "select",
    options: ["false", "true"],
    hidden: true,
  },
  {
    key: "editor_minimap",
    default: "true",
    type: "select",
    options: ["false", "true"],
    hidden: true,
  },
]
export type LocalSetting = (typeof initialLocalSettings)[number]
for (const setting of initialLocalSettings) {
  if (!local[setting.key]) {
    setLocal(setting.key, setting.default)
  }
}

export { local, setLocal, remove, clear, toJSON }
