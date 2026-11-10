import { IconButton } from "@hope-ui/solid"
import { changeColor } from "seemly"
import { BsGridFill } from "solid-icons/bs"
import { FaSolidListUl } from "solid-icons/fa"
import { Show } from "solid-js"
import { useT } from "~/hooks"
import { getMainColor, layout, setLayout } from "~/store"

export const Layout = () => {
  const t = useT()
  return (
    <IconButton
      color={getMainColor()}
      bgColor={changeColor(getMainColor(), { alpha: 0.15 })}
      _hover={{ bgColor: changeColor(getMainColor(), { alpha: 0.2 }) }}
      aria-label={t(
        layout() === "list" ? "home.layouts.grid" : "home.layouts.list",
      )}
      title={t(layout() === "list" ? "home.layouts.grid" : "home.layouts.list")}
      compact
      size="lg"
      onClick={() => setLayout(layout() === "list" ? "grid" : "list")}
      icon={
        <Show when={layout() === "list"} fallback={<BsGridFill />}>
          <FaSolidListUl />
        </Show>
      }
    />
  )
}
