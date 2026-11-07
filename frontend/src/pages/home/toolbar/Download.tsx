import {
  Menu,
  MenuTrigger,
  MenuContent,
  MenuItem,
  Modal,
  ModalContent,
  ModalHeader,
  ModalOverlay,
  createDisclosure,
} from "@hope-ui/solid"
import { createMemo, lazy, onCleanup, Show, Suspense } from "solid-js"
import { FullLoading } from "~/components"
import { useT, useDownload } from "~/hooks"
import { getSettingBool, me, selectedObjs } from "~/store"
import { UserMethods } from "~/types"
import { bus } from "~/utils"
import { CenterIcon } from "./Icon"

export const Download = () => {
  const t = useT()
  const colorScheme = "neutral"
  const { batchDownloadSelected } = useDownload()

  const objs = () => selectedObjs()
  const hasFile = createMemo(() => objs().some((obj) => !obj.is_dir))
  const canPackage = createMemo(
    () => UserMethods.is_admin(me()) || getSettingBool("package_download"),
  )
  const canPackageOption = createMemo(
    () => canPackage() && (objs().length > 1 || objs().some((o) => o.is_dir)),
  )

  const showMenu = createMemo(() => hasFile() && canPackageOption())

  const handleDirectDownload = () => {
    if (hasFile()) {
      batchDownloadSelected()
    } else if (canPackageOption()) {
      bus.emit("tool", "package_download")
    }
  }

  return (
    <Show
      when={showMenu()}
      fallback={<CenterIcon name="download" onClick={handleDirectDownload} />}
    >
      <Menu placement="top" offset={10}>
        <MenuTrigger as={CenterIcon} name="download" />
        <MenuContent>
          <MenuItem colorScheme={colorScheme} onSelect={batchDownloadSelected}>
            {t("home.toolbar.batch_download")}
          </MenuItem>
          <MenuItem
            colorScheme={colorScheme}
            onSelect={() => {
              bus.emit("tool", "package_download")
            }}
          >
            {t("home.toolbar.package_download")}
          </MenuItem>
        </MenuContent>
      </Menu>
    </Show>
  )
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
