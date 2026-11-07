import { Box, HStack, useColorModeValue } from "@hope-ui/solid"
import { createMemo, Show } from "solid-js"
import {
  checkboxOpen,
  haveSelected,
  me,
  objStore,
  oneChecked,
  selectAll,
  selectedObjs,
  State,
  userCan,
  getSettingBool,
} from "~/store"
import { isArchive } from "~/store/archive"
import { UserMethods } from "~/types"
import { CenterIcon } from "./Icon"
import { bus } from "~/utils"
import { Download } from "./Download"
import { Motion, Presence } from "solid-motionone"
import { useRouter } from "~/hooks"

export const Center = () => {
  const show = createMemo(
    () =>
      [State.Folder, State.FetchingMore].includes(objStore.state) &&
      checkboxOpen() &&
      haveSelected(),
  )
  const { isShare } = useRouter()

  const hasDir = createMemo(() => selectedObjs().some((o) => o.is_dir))
  const hasFile = createMemo(() => selectedObjs().some((o) => !o.is_dir))
  const canPackageDownload = createMemo(
    () => UserMethods.is_admin(me()) || getSettingBool("package_download"),
  )

  // 1. 重命名 (rename): 只有当且仅当选中 1 个文件或 1 个文件夹时才显示（多选无法单独重命名）
  const canRename = createMemo(
    () => !isShare() && objStore.write && userCan("rename") && oneChecked(),
  )

  // 2. 移动 (move): 任何选中的文件/文件夹均可移动
  const canMove = createMemo(
    () => !isShare() && objStore.write && userCan("move"),
  )

  // 3. 复制 (copy): 任何选中的文件/文件夹均可复制
  const canCopy = createMemo(
    () => !isShare() && objStore.write && userCan("copy"),
  )

  // 4. 删除 (delete): 任何选中的文件/文件夹均可删除
  const canDelete = createMemo(
    () => !isShare() && objStore.write && userCan("delete"),
  )

  // 5. 解压缩 (decompress): 只有所选对象全部为压缩文件时显示（排除文件夹及非压缩文件）
  const canDecompress = createMemo(
    () =>
      !isShare() &&
      objStore.write &&
      userCan("decompress") &&
      !hasDir() &&
      selectedObjs().length > 0 &&
      selectedObjs().every((o) => isArchive(o.name)),
  )

  // 6. 分享 (share): 任何选中的文件/文件夹均可生成分享
  const canShare = createMemo(() => !isShare() && userCan("share"))

  // 7. 下载 (download): 选中有文件可直接下载，或支持打包下载时显示
  const canDownload = createMemo(() => hasFile() || canPackageDownload())

  return (
    <Presence exitBeforeEnter>
      <Show when={show()}>
        <Box
          class="center-toolbar"
          pos="fixed"
          bottom="$4"
          left="50%"
          w="max-content"
          color="$neutral11"
          transform="translateX(-50%)"
        >
          <Box
            as={Motion.div}
            initial={{ opacity: 0, scale: 0.9, y: 10 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.9 }}
            // @ts-ignore
            transition={{ duration: 0.2 }}
          >
            <HStack
              p="$2"
              bgColor={useColorModeValue("white", "#000000d0")()}
              spacing="$1"
              shadow="0px 10px 30px -5px rgba(0, 0, 0, 0.3)"
              rounded="$lg"
              css={{
                backdropFilter: "blur(8px)",
              }}
            >
              <Show when={canRename()}>
                <CenterIcon
                  name="rename"
                  onClick={() => {
                    bus.emit("tool", "rename")
                  }}
                />
              </Show>
              <Show when={canMove()}>
                <CenterIcon
                  name="move"
                  onClick={() => {
                    bus.emit("tool", "move")
                  }}
                />
              </Show>
              <Show when={canCopy()}>
                <CenterIcon
                  name="copy"
                  onClick={() => {
                    bus.emit("tool", "copy")
                  }}
                />
              </Show>
              <Show when={canDelete()}>
                <CenterIcon
                  name="delete"
                  onClick={() => {
                    bus.emit("tool", "delete")
                  }}
                />
              </Show>
              <Show when={canDecompress()}>
                <CenterIcon
                  name="decompress"
                  onClick={() => {
                    bus.emit("tool", "decompress")
                  }}
                />
              </Show>
              <Show when={canShare()}>
                <CenterIcon
                  name="share"
                  onClick={() => {
                    bus.emit("tool", "share")
                  }}
                />
              </Show>
              <Show when={canDownload()}>
                <Download />
              </Show>
              <CenterIcon
                name="cancel_select"
                onClick={() => {
                  selectAll(false)
                }}
              />
            </HStack>
          </Box>
        </Box>
      </Show>
    </Presence>
  )
}
