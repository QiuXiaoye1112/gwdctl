import {
  VStack,
  HStack,
  Switch,
} from "@hope-ui/solid"
import { JSXElement, createSignal, createMemo, Show } from "solid-js"
import { useRouter, useT, usePath, getGlobalPage } from "~/hooks"
import { getPagination, objStore } from "~/store"
import { ObjType } from "~/types"
import { pathDir } from "~/utils"
import Artplayer from "artplayer"
import { SelectWrapper } from "~/components"

Artplayer.PLAYBACK_RATE = [0.5, 0.75, 1, 1.25, 1.5, 2, 3, 4]
Artplayer.REMOVE_SRC_WHEN_DESTROY = true

export const AutoHeightPlugin = (player: Artplayer) => {
  const { $container, $video } = player.template
  const $videoBox = $container.parentElement!

  player.on("ready", () => {
    const offsetBottom = "1.75rem"
    $videoBox.style.maxHeight = `calc(100vh - ${$videoBox.offsetTop}px - ${offsetBottom})`
    $videoBox.style.minHeight = "320px"
    player.autoHeight()
  })
  player.on("resize", () => {
    player.autoHeight()
  })
  player.on("error", () => {
    if ($video.style.height) return
    $container.style.height = "60vh"
    $video.style.height = "100%"
  })
}

export const VideoBox = (props: {
  children: JSXElement
  onAutoNextChange: (v: boolean) => void
}) => {
  const { replace, pathname } = useRouter()
  const { handleFolder } = usePath()
  const [videoName, setVideoName] = createSignal("")
  const videos = createMemo(() => {
    let isLoadMore = true,
      isLast = false
    const videos = objStore.objs.filter((obj) => {
      if (obj.type !== ObjType.VIDEO) return false
      if (obj.name === objStore.obj.name) {
        isLoadMore = false
        isLast = true
        setVideoName(obj.name)
      } else isLast = false
      return true
    })
    if (isLast) {
      isLoadMore = getPagination().type !== "all"
    }
    if (isLoadMore) {
      let path = pathname()
      if (!path.endsWith(objStore.obj.name)) {
        videos.push(objStore.obj)
        setVideoName(objStore.obj.name)
        return videos
      }
      const append = objStore.objs.length > 0
      handleFolder(
        pathDir(path),
        getGlobalPage() + (append ? 1 : 0),
        undefined,
        append,
        false,
        true,
      )
    }
    return videos
  })
  const t = useT()
  let autoNext = localStorage.getItem("video_auto_next")
  if (!autoNext) {
    autoNext = "true"
  }
  props.onAutoNextChange(autoNext === "true")

  return (
    <VStack w="$full" spacing="$2">
      {props.children}
      <Show when={videoName() !== ""}>
        <HStack spacing="$2" w="$full">
          <SelectWrapper
            onChange={(name: string) => {
              replace(name)
            }}
            value={videoName()}
            options={videos().map((obj) => ({ value: obj.name }))}
          />
          <Switch
            css={{
              whiteSpace: "nowrap",
            }}
            defaultChecked={autoNext === "true"}
            onChange={(e: { currentTarget: HTMLInputElement }) => {
              props.onAutoNextChange(e.currentTarget.checked)
              localStorage.setItem(
                "video_auto_next",
                e.currentTarget.checked.toString(),
              )
            }}
          >
            {t("home.preview.auto_next")}
          </Switch>
        </HStack>
      </Show>
    </VStack>
  )
}
