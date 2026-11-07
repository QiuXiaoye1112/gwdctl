import { useFetch, useRouter, useT, useUtil } from "~/hooks"
import {
  bus,
  getExpireDate,
  handleResp,
  shareMessage,
  notify,
  r,
  randomPwd,
} from "~/utils"
import { batch, createSignal, Match, onCleanup, Switch } from "solid-js"
import {
  Button,
  createDisclosure,
  HStack,
  IconButton,
  Input,
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Text,
  Textarea,
  VStack,
} from "@hope-ui/solid"
import {
  PResp,
  Share as ShareType,
  ShareInfo,
} from "~/types"
import { createStore } from "solid-js/store"
import { getSetting, me, selectedObjs } from "~/store"
import { TbRefresh } from "solid-icons/tb"
import { MultiPathInput } from "~/components"

export const Share = () => {
  const t = useT()
  const { isOpen, onOpen, onClose } = createDisclosure()
  const { copy } = useUtil()
  const [link, setLink] = createSignal("")
  const [expireString, setExpireString] = createSignal("")
  const [expireValid, setExpireValid] = createSignal(true)
  const [share, setShare] = createStore<ShareType>({} as ShareType)
  const { pathname } = useRouter()
  const handler = (name: string) => {
    if (name === "share") {
      batch(() => {
        setLink("")
        setExpireString("")
        setExpireValid(true)
        const paths = selectedObjs().map((obj) => {
          const split =
            pathname().endsWith("/") || obj.name.startsWith("/") ? "" : "/"
          return `${me().base_path}${pathname()}${split}${obj.name}`
        })
        setShare({
          files: paths,
          expires: null,
          pwd: "",
          max_accessed: 0,
          remark: "",
          readme: "",
          header: "",
        } as ShareType)
      })
      onOpen()
    }
  }
  bus.on("tool", handler)
  onCleanup(() => {
    bus.off("tool", handler)
  })
  const [okLoading, ok] = useFetch((): PResp<ShareInfo> => {
    return r.post(`/share/create`, share)
  })
  return (
    <Modal
      blockScrollOnMount={false}
      opened={isOpen()}
      onClose={onClose}
      size={{
        "@initial": "xs",
        "@md": "md",
        "@lg": "lg",
        "@xl": "xl",
        "@2xl": "2xl",
      }}
    >
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>{t("home.toolbar.share")}</ModalHeader>
        <Switch
          fallback={
            <>
              <ModalBody>
                <Textarea variant="filled" value={link()} readonly />
              </ModalBody>
              <ModalFooter display="flex" gap="$2">
                <Button
                  colorScheme="primary"
                  onClick={() => {
                    copy(link())
                  }}
                >
                  {t("shares.copy_msg")}
                </Button>
                <Button colorScheme="info" onClick={onClose}>
                  {t("global.confirm")}
                </Button>
              </ModalFooter>
            </>
          }
        >
          <Match when={link() === ""}>
            <ModalBody>
              <VStack spacing="$1" alignItems="flex-start">
                <Text size="sm">{t("shares.id")}</Text>
                <Input
                  size="sm"
                  value={share.id ?? ""}
                  maxLength={64}
                  placeholder={t("shares.id_placeholder")}
                  onInput={(e) => {
                    setShare("id", e.currentTarget.value)
                  }}
                />
                <Text size="sm">{t("shares.remark")}</Text>
                <Textarea
                  size="sm"
                  value={share.remark}
                  onInput={(e) => {
                    setShare("remark", e.currentTarget.value)
                  }}
                />
                <Text size="sm">{t("shares.pwd")}</Text>
                <HStack spacing="$1" w="$full">
                  <Input
                    size="sm"
                    value={share.pwd}
                    onInput={(e) => {
                      setShare("pwd", e.currentTarget.value)
                    }}
                  />
                  <IconButton
                    colorScheme="neutral"
                    size="sm"
                    aria-label="random"
                    icon={<TbRefresh />}
                    onClick={() => {
                      setShare("pwd", randomPwd())
                    }}
                  />
                </HStack>
                <Text size="sm">{t("shares.expires")}</Text>
                <Input
                  size="sm"
                  invalid={!expireValid()}
                  value={expireString()}
                  placeholder="yyyy-MM-dd HH:mm:ss or +1w1d1H1m1s1ms"
                  onInput={(e) => {
                    setExpireString(e.currentTarget.value)
                    if (e.currentTarget.value === "") {
                      setExpireValid(true)
                      setShare("expires", null)
                      return
                    }
                    const date = getExpireDate(e.currentTarget.value)
                    if (isNaN(date.getTime())) {
                      setExpireValid(false)
                    } else {
                      setExpireValid(true)
                      setShare("expires", date.toISOString())
                    }
                  }}
                />
                <Text size="sm">{t("shares.readme")}</Text>
                <Textarea
                  size="sm"
                  value={share.readme}
                  onInput={(e) => {
                    setShare("readme", e.currentTarget.value)
                  }}
                />
              </VStack>
            </ModalBody>
            <ModalFooter display="flex" gap="$2">
              <Button colorScheme="neutral" onClick={onClose}>
                {t("global.cancel")}
              </Button>
              <Button
                colorScheme="info"
                disabled={!expireValid()}
                loading={okLoading()}
                onClick={async () => {
                  const resp = await ok()
                  handleResp(resp, (data) => {
                    const msg = shareMessage(data, getSetting("share_summary_content"), getSetting("site_title"))
                    setLink(msg)
                    copy(msg)
                    notify.success("分享链接已创建并自动复制到剪贴板！")
                  })
                }}
              >
                {t("global.confirm")}
              </Button>
            </ModalFooter>
          </Match>
        </Switch>
      </ModalContent>
    </Modal>
  )
}
