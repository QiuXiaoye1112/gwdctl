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
  const { pathname } = useRouter()
  const { refresh } = usePath()
  const [pendingRename, setPendingRename] = createSignal<{
    srcPath: string
    name: string
  } | null>(null)

  const closeOverwriteModal = () => {
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
          loading={loading()}
          onSubmit={async (name) => {
            const srcObj = selectedObjs()[0]
            if (!srcObj) {
              onClose()
              return
            }
            const srcPath = pathJoin(pathname(), srcObj.name)
            const resp = await ok(srcPath, name, false, false)
            onClose()
            if (resp.code === 409) {
              setPendingRename({ srcPath, name })
              onOverwriteOpen()
              return
            }
            handleRespWithNotifySuccess(resp, () => {
              refresh(false, true)
            })
          }}
        />
      </Show>
      <Modal
        blockScrollOnMount={false}
        opened={isOverwriteOpen()}
        onClose={closeOverwriteModal}
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
            <Button onClick={closeOverwriteModal} colorScheme="neutral">
              {t("global.cancel")}
            </Button>
            <Button
              colorScheme="danger"
              loading={loading()}
              onClick={async () => {
                const pending = pendingRename()
                if (!pending) {
                  closeOverwriteModal()
                  return
                }
                const resp = await ok(
                  pending.srcPath,
                  pending.name,
                  true,
                  false,
                )
                closeOverwriteModal()
                handleRespWithNotifySuccess(resp, () => {
                  refresh(false, true)
                })
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
