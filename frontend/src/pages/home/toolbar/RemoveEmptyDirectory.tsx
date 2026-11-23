import {
  Button,
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  createDisclosure,
} from "@hope-ui/solid"
import { useFetch, usePath, useRouter, useT } from "~/hooks"
import {
  bus,
  fsRemoveEmptyDirectory,
  handleRespWithNotifySuccess,
} from "~/utils"
import { createSignal, onCleanup } from "solid-js"

export const RemoveEmptyDirectory = () => {
  const { isOpen, onOpen, onClose } = createDisclosure()
  const { pathname } = useRouter()
  const [loading, ok] = useFetch(fsRemoveEmptyDirectory)
  const [submitting, setSubmitting] = createSignal(false)
  const busy = () => loading() || submitting()
  const { refresh } = usePath()
  const handleClose = () => {
    if (busy()) return
    onClose()
  }
  const handler = (name: string) => {
    if (name === "removeEmptyDirectory") {
      onOpen()
    }
  }
  bus.on("tool", handler)
  onCleanup(() => {
    bus.off("tool", handler)
  })
  const t = useT()
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
        <ModalHeader>{t("home.toolbar.remove_empty_directory")}</ModalHeader>
        <ModalBody>
          <p>{t("home.toolbar.remove_empty_directory-tips")}</p>
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
              setSubmitting(true)
              try {
                const resp = await ok(dir)
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
