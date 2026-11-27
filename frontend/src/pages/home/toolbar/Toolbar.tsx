import { Portal } from "solid-js/web"
import { Center } from "./Center"
import { Right } from "./Right"
import { Copy, Move } from "./CopyMove"
import { Delete } from "./Delete"
import { Rename } from "./Rename"
import { Mkdir } from "./Mkdir"
import { RemoveEmptyDirectory } from "./RemoveEmptyDirectory"
import { DirectDownloadModal, PackageDownloadModal } from "./Download"
import { lazy, Show } from "solid-js"
import { objStore, State } from "~/store"
import { ModalWrapper } from "./ModalWrapper"
import { LocalSettings } from "./LocalSettings"
import { TasksDrawer } from "./TasksDrawer"
import { BackTop } from "./BackTop"
import { Share } from "./Share"
import { UploadConflictModal } from "../uploads/UploadConflictModal"

const Upload = lazy(() => import("../uploads/Upload"))

export const Modal = () => {
  return (
    <>
      <Copy />
      <Move />
      <Rename />
      <Delete />
      <Mkdir />
      <Share />
      <RemoveEmptyDirectory />
      <DirectDownloadModal />
      <PackageDownloadModal />
      <ModalWrapper name="upload" title="home.toolbar.upload">
        <Upload />
      </ModalWrapper>
      <UploadConflictModal />
      <LocalSettings />
      <TasksDrawer />
    </>
  )
}

export const Toolbar = () => {
  return (
    <Portal>
      <Show when={[State.Folder, State.FetchingMore].includes(objStore.state)}>
        <Right />
      </Show>
      <Center />
      <Modal />
      <BackTop />
    </Portal>
  )
}
