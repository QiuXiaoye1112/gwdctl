import {
  Modal,
  ModalOverlay,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Button,
  Checkbox,
  createDisclosure,
} from "@hope-ui/solid"
import { createSignal, onCleanup } from "solid-js"
import { useFetch, usePath, useRouter, useT } from "~/hooks"
import { selectedObjs } from "~/store"
import { bus, fsRemove, handleRespWithNotifySuccess } from "~/utils"

export const Delete = () => {
  const t = useT()
  const { isOpen, onOpen, onClose } = createDisclosure()
  const [loading, ok] = useFetch(fsRemove)
  const [submitting, setSubmitting] = createSignal(false)
  const busy = () => loading() || submitting()
  const { refresh } = usePath()
  const { pathname } = useRouter()
  const [followSeed, setFollowSeed] = createSignal(false)
  const handleClose = () => {
    if (busy()) return
    onClose()
  }
  const handler = (name: string) => {
    if (name === "delete") {
      setFollowSeed(false)
      onOpen()
    }
  }
  bus.on("tool", handler)
  onCleanup(() => {
    bus.off("tool", handler)
  })
  return (
    <Modal
      blockScrollOnMount={false}
      opened={isOpen()}
      onClose={handleClose}
      closeOnOverlayClick={!busy()}
      closeOnEsc={!busy()}
      size={{
        "@initial": "xs",
        "@md": "md",
      }}
    >
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>{t("home.toolbar.delete")}</ModalHeader>
        <ModalBody>
          <p>{t("home.toolbar.delete-tips")}</p>
        </ModalBody>
        <ModalFooter display="flex" gap="$2">
          <Button
            disabled={busy()}
            onClick={handleClose}
            colorScheme="neutral"
          >
            {t("global.cancel")}
          </Button>
          <Button
            colorScheme="danger"
            loading={busy()}
            onClick={async () => {
              if (busy()) return
              const dir = pathname()
              const names = selectedObjs().map((obj) => obj.name)
              setSubmitting(true)
              try {
                const resp = await ok(dir, names, false)
                let succeeded = false
                handleRespWithNotifySuccess(resp, () => {
                  succeeded = true
                })
                if (succeeded) {
                  await refresh(false, true)
                  onClose()
                }
              } finally {
                setSubmitting(false)
              }
            }}
          >
            {t("global.confirm")}
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}
