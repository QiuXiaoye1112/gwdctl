import {
  Box,
  Button,
  HStack,
  Table,
  Tbody,
  Th,
  Thead,
  Tr,
  VStack,
} from "@hope-ui/solid"
import { createSignal, For } from "solid-js"
import { useFetch, useManageTitle, useRouter, useT } from "~/hooks"
import { PageResp, ShareInfo } from "~/types"
import { handleResp, r } from "~/utils"
import { ShareListItem } from "./Share"

const Shares = () => {
  const t = useT()
  useManageTitle("manage.sidemenu.shares")
  const { to } = useRouter()
  const [getSharesLoading, getShares] = useFetch(
    (): Promise<PageResp<ShareInfo>> => r.get("/share/list"),
  )
  const [shares, setShares] = createSignal<ShareInfo[]>([])
  const refresh = async () => {
    const resp = await getShares()
    handleResp(resp, (data) => setShares(data.content))
  }
  refresh()
  return (
    <VStack spacing="$3" alignItems="start" w="$full">
      <HStack
        spacing="$2"
        gap="$2"
        w="$full"
        wrap={{
          "@initial": "wrap",
          "@md": "unset",
        }}
      >
        <Button
          colorScheme="accent"
          loading={getSharesLoading()}
          onClick={refresh}
        >
          {t("global.refresh")}
        </Button>
        <Button onClick={() => to("/@manage/shares/add")}>
          {t("global.add")}
        </Button>
      </HStack>
      <Box w="$full" overflowX="auto">
        <Table highlightOnHover dense>
          <Thead>
            <Tr>
              <For each={["files", "id", "expires", "accessed", "status", "remark"]}>
                {(title) => <Th>{t(`shares.${title}`)}</Th>}
              </For>
              <Th>{t("global.operations")}</Th>
            </Tr>
          </Thead>
          <Tbody>
            <For each={shares()}>
              {(share) => (
                <ShareListItem
                  share={share}
                  refresh={refresh}
                />
              )}
            </For>
          </Tbody>
        </Table>
      </Box>
    </VStack>
  )
}

export default Shares
