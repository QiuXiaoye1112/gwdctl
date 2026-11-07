import {
  Modal,
  ModalContent,
  ModalHeader,
  ModalOverlay,
  createDisclosure,
} from "@hope-ui/solid"
import { lazy, onCleanup, Show, Suspense } from "solid-js"
import { FullLoading } from "~/components"
import { useDownload, useT } from "~/hooks"
import { getSettingBool, oneChecked, selectedObjs } from "~/store"
import { bus } from "~/utils"
import { CenterIcon } from "./Icon"

export const Download = () => {
  const { downloadSelected } = useDownload()

  const handleDownload = () => {
    if (oneChecked() && !selectedObjs()[0].is_dir) {
      downloadSelected()
    } else {
      bus.emit("tool", "package_download")
    }
  }

  return <CenterIcon name="download" onClick={handleDownload} />
}

const PackageDownload = lazy(() => import("./PackageDownload"))

export const PackageDownloadModal = () => {
  const t = useT()
  const { isOpen, onOpen, onClose } = createDisclosure()
  const handler = (name: string) => {
    if (name === "package_download") {
      if (!getSettingBool("package_download")) return
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
      onClose={onClose}
      closeOnOverlayClick={false}
      closeOnEsc={false}
    >
      <ModalOverlay />
      <ModalContent>
        <ModalHeader>{t("home.toolbar.package_download")}</ModalHeader>
        <Suspense fallback={<FullLoading />}>
          <Show when={isOpen()}>
            <PackageDownload onClose={onClose} />
          </Show>
        </Suspense>
      </ModalContent>
    </Modal>
  )
}
