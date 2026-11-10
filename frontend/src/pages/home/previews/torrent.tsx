import { Box, Heading, Text, VStack } from "@hope-ui/solid"
import { createSignal, For, onMount, Show } from "solid-js"
import { useLink, useT } from "~/hooks"
import { objStore } from "~/store"
import { getFileSize } from "~/utils"
import { parseTorrent } from "./torrent_parse.js"

type TorrentPreview = ReturnType<typeof parseTorrent>

const Preview = () => {
  const t = useT()
  const { proxyLink, rawLink } = useLink()
  const [info, setInfo] = createSignal<TorrentPreview>()
  const [loading, setLoading] = createSignal(true)
  const [failed, setFailed] = createSignal(false)

  onMount(async () => {
    try {
      let response = await fetch(proxyLink(objStore.obj, true))
      if (!response.ok) response = await fetch(rawLink(objStore.obj, true))
      if (!response.ok) throw new Error("File request failed")
      setInfo(parseTorrent(new Uint8Array(await response.arrayBuffer())))
    } catch {
      setFailed(true)
    } finally {
      setLoading(false)
    }
  })

  return (
    <VStack spacing="$3" alignItems="stretch" w="$full" p="$4">
      <Show when={loading()}>
        <Text>{t("home.torrent_readonly.loading")}</Text>
      </Show>
      <Show when={failed()}>
        <Text color="$danger9">{t("home.torrent_readonly.failed")}</Text>
      </Show>
      <Show when={info()}>
        {(data) => (
          <>
            <Heading size="sm" css={{ "word-break": "break-all" }}>
              {data().name}
            </Heading>
            <Text>
              {t("home.torrent_readonly.summary", {
                count: data().files.length,
                size: getFileSize(data().totalSize),
              })}
            </Text>
            <Text css={{ "word-break": "break-all" }}>
              Info hash: {data().infoHash}
            </Text>
            <Text>
              {t("home.torrent_readonly.pieces", {
                count: data().pieceCount,
                size: getFileSize(data().pieceSize),
              })}
            </Text>
            <Box w="$full">
              <For each={data().files}>
                {(file) => (
                  <Text py="$1" css={{ "word-break": "break-all" }}>
                    {file.path} — {getFileSize(file.size)}
                  </Text>
                )}
              </For>
            </Box>
          </>
        )}
      </Show>
    </VStack>
  )
}

export default Preview
