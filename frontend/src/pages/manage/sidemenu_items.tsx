import { SideMenuItemProps } from "./SideMenu"
import {
  BsFingerprint,
  BsCloudUploadFill,
  BsSearch,
} from "solid-icons/bs"
import { CgShare } from "solid-icons/cg"
import { OcWorkflow2 } from "solid-icons/oc"
import { IoCopy, IoMove, IoHome } from "solid-icons/io"
import { Component, lazy } from "solid-js"
import { UserRole } from "~/types"
import { TbArchive } from "solid-icons/tb"

export type SideMenuItem = SideMenuItemProps & {
  component?: Component
  children?: SideMenuItem[]
}

export const side_menu_items: SideMenuItem[] = [
  {
    title: "manage.sidemenu.profile",
    icon: BsFingerprint,
    to: "/@manage",
    role: UserRole.GUEST,
    component: lazy(() => import("./users/Profile")),
  },
  {
    title: "manage.sidemenu.tasks",
    icon: OcWorkflow2,
    to: "/@manage/tasks",
    role: UserRole.GENERAL,
    backend: ["go"],
    children: [
      {
        title: "manage.sidemenu.upload",
        icon: BsCloudUploadFill,
        to: "/@manage/tasks/upload",
        role: UserRole.GENERAL,
        component: lazy(() => import("./tasks/Upload")),
      },
      {
        title: "manage.sidemenu.copy",
        icon: IoCopy,
        to: "/@manage/tasks/copy",
        role: UserRole.GENERAL,
        component: lazy(() => import("./tasks/Copy")),
      },
      {
        title: "manage.sidemenu.move",
        icon: IoMove,
        to: "/@manage/tasks/move",
        role: UserRole.GENERAL,
        component: lazy(() => import("./tasks/Move")),
      },
      {
        title: "manage.sidemenu.decompress",
        icon: TbArchive,
        to: "/@manage/tasks/decompress",
        role: UserRole.GENERAL,
        component: lazy(() => import("./tasks/Decompress")),
      },
    ],
  },
  {
    title: "manage.sidemenu.shares",
    icon: CgShare,
    to: "/@manage/shares",
    role: UserRole.GENERAL,
    component: lazy(() => import("./shares/Shares")),
  },
  {
    title: "manage.sidemenu.indexes",
    icon: BsSearch,
    to: "/@manage/indexes",
    component: lazy(() => import("./indexes/index_page")),
  },
  {
    title: "manage.sidemenu.home",
    icon: IoHome,
    to: "/",
    role: UserRole.GUEST,
    refresh: true,
  },
]
