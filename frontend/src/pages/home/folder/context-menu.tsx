import { Menu, Item, Submenu } from "solid-contextmenu"
import { useDownload, useRouter, useT } from "~/hooks"
import "solid-contextmenu/dist/style.css"
import { HStack, Icon, Text, useColorMode } from "@hope-ui/solid"
import { operations } from "../toolbar/operations"
import { createMemo, For, Show } from "solid-js"
import { bus, notify } from "~/utils"
import { UserMethods } from "~/types"
import {
  getSettingBool,
  haveSelected,
  me,
  objStore,
  oneChecked,
  selectedObjs,
  userCan,
} from "~/store"
import { getPreviews } from "../previews"
import { isArchive } from "~/store/archive"

const ItemContent = (props: { name: string }) => {
  const t = useT()
  return (
    <HStack spacing="$2">
      <Icon
        p={operations[props.name].p ? "$1" : undefined}
        as={operations[props.name].icon}
        boxSize="$7"
        color={operations[props.name].color}
      />
      <Text>{t(`home.toolbar.${props.name}`)}</Text>
    </HStack>
  )
}

export const ContextMenu = () => {
  const t = useT()
  const { colorMode } = useColorMode()
  const { downloadSelected } = useDownload()
  const canPackageDownload = () => {
    return UserMethods.is_admin(me()) || getSettingBool("package_download")
  }
  const { isShare, pushHref, to } = useRouter()
  const openWithPreviews = createMemo(() => {
    const objs = selectedObjs()
    if (objs.length !== 1) return []
    const obj = objs[0]
    if (obj.is_dir) return []
    return getPreviews({ ...obj, provider: objStore.provider })
  })
  return (
    <Menu
      id={1}
      animation="scale"
      theme={colorMode() !== "dark" ? "light" : "dark"}
      style="z-index: var(--hope-zIndices-popover)"
    >
      <Show when={openWithPreviews().length > 0}>
        <Submenu label={<ItemContent name="open_with" />}>
          <For each={openWithPreviews()}>
            {(preview) => (
              <Item
                onClick={({ props }) => {
                  to(`${pushHref(props.name)}?preview=${preview.key}`)
                }}
              >
                {preview.name}
              </Item>
            )}
          </For>
        </Submenu>
      </Show>
      <For each={["rename", "move", "copy", "delete"] as const}>
        {(name) => (
          <Item
            hidden={
              !userCan(name) ||
              !objStore.write ||
              isShare() ||
              (name === "rename" && !oneChecked())
            }
            onClick={() => {
              bus.emit("tool", name)
            }}
          >
            <ItemContent name={name} />
          </Item>
        )}
      </For>
      <Item
        hidden={!userCan("share") || isShare()}
        onClick={() => {
          bus.emit("tool", "share")
        }}
      >
        <ItemContent name="share" />
      </Item>
      <Item
        hidden={() => {
          return (
            isShare() ||
            !userCan("decompress") ||
            !objStore.write ||
            selectedObjs().some((o) => o.is_dir) ||
            selectedObjs().some((o) => !isArchive(o.name))
          )
        }}
        onClick={() => {
          bus.emit("tool", "decompress")
        }}
      >
        <ItemContent name="decompress" />
      </Item>
      <Show when={oneChecked() || selectedObjs().some((o) => o.is_dir)}>
        <Item
          onClick={({ props }) => {
            if (props.is_dir || selectedObjs().some((o) => o.is_dir)) {
              if (!canPackageDownload()) {
                notify.warning(t("home.toolbar.package_download_disabled"))
                return
              }
              bus.emit("tool", "package_download")
            } else {
              bus.emit("tool", "direct_download")
            }
          }}
        >
          <ItemContent
            name={
              selectedObjs().some((o) => o.is_dir)
                ? "package_download"
                : "download"
            }
          />
        </Item>
      </Show>
      <Show
        when={
          !oneChecked() &&
          haveSelected() &&
          !selectedObjs().some((o) => o.is_dir)
        }
      >
        <Item onClick={() => bus.emit("tool", "direct_download")}>
          <ItemContent name="direct_download" />
        </Item>
        <Show when={canPackageDownload()}>
          <Item onClick={() => bus.emit("tool", "package_download")}>
            <ItemContent name="package_download" />
          </Item>
        </Show>
      </Show>
    </Menu>
  )
}
