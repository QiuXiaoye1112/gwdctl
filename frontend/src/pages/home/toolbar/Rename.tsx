import {
  Button,
  createDisclosure,
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
} from "@hope-ui/solid"
import { createSignal, onCleanup, Show } from "solid-js"
import { ModalInput } from "~/components"
import { useFetch, usePath, useRouter, useT } from "~/hooks"
import { oneChecked, selectedObjs } from "~/store"
import { bus, fsRename, handleRespWithNotifySuccess, pathJoin } from "~/utils"

export const Rename = () => {
  const t = useT()
  const { isOpen, onOpen, onClose } = createDisclosure()
  const {
    isOpen: isOverwriteOpen,
    onOpen: onOverwriteOpen,
    onClose: onOverwriteClose,
  } = createDisclosure()
  const [loading, ok] = useFetch(fsRename)
  const [submitting, setSubmitting] = createSignal(false)
  const busy = () => loading() || submitting()
  const { pathname } = useRouter()
  const { refresh } = usePath()
  const [pendingRename, setPendingRename] = createSignal<{
    srcPath: string
    name: string
  } | null>(null)

  const closeOverwriteModal = () => {
    if (busy()) return
    setPendingRename(null)
    onOverwriteClose()
  }

  const handler = (name: string) => {
    if (name === "rename") {
      if (!oneChecked()) {
        return
      }
      setPendingRename(null)
      onOverwriteClose()
      onOpen()
    }
  }
  bus.on("tool", handler)
  onCleanup(() => {
    bus.off("tool", handler)
  })
  return (
    <>
      <Show when={isOpen()}>
        <ModalInput
          title="home.toolbar.input_new_name"
          validateFilename={true}
          isRenamingFile={!selectedObjs()[0]?.is_dir}
          opened={isOpen()}
          onClose={onClose}
          defaultValue={selectedObjs()[0]?.name ?? ""}
          loading={busy()}
          onSubmit={async (name) => {
            if (busy()) return
            const srcObj = selectedObjs()[0]
            if (!srcObj) {
              onClose()
              return
            }
            const srcPath = pathJoin(pathname(), srcObj.name)
            setSubmitting(true)
            try {
              const resp = await ok(srcPath, name, false, false)
              if (resp.code === 409) {
                onClose()
                setPendingRename({ srcPath, name })
                onOverwriteOpen()
                return
              }
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
        />
      </Show>
      <Modal
        blockScrollOnMount={false}
        opened={isOverwriteOpen()}
        onClose={closeOverwriteModal}
        closeOnOverlayClick={!busy()}
        closeOnEsc={!busy()}
        size={{
          "@initial": "xs",
          "@md": "md",
        }}
      >
        <ModalOverlay />
        <ModalContent>
          <ModalHeader>{t("home.toolbar.rename_overwrite_title")}</ModalHeader>
          <ModalBody>
            <p>
              {t("home.toolbar.rename_overwrite_tips", {
                name: pendingRename()?.name ?? "",
              })}
            </p>
          </ModalBody>
          <ModalFooter display="flex" gap="$2">
            <Button
              disabled={busy()}
              onClick={closeOverwriteModal}
              colorScheme="neutral"
            >
              {t("global.cancel")}
            </Button>
            <Button
              colorScheme="danger"
              loading={busy()}
              onClick={async () => {
                if (busy()) return
                const pending = pendingRename()
                if (!pending) {
                  setPendingRename(null)
                  onOverwriteClose()
                  return
                }
                setSubmitting(true)
                try {
                  const resp = await ok(
                    pending.srcPath,
                    pending.name,
                    true,
                    false,
                  )
                  let succeeded = false
                  handleRespWithNotifySuccess(resp, () => {
                    succeeded = true
                  })
                  if (succeeded) {
                    await refresh(false, true)
                    setPendingRename(null)
                    onOverwriteClose()
                  }
                } finally {
                  setSubmitting(false)
                }
              }}
            >
              {t("home.toolbar.rename_overwrite_confirm")}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  )
}
