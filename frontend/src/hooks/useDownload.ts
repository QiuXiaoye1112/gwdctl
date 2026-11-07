import { useSelectedLink } from "~/hooks"

export const useDownload = () => {
  const { rawLinks } = useSelectedLink()
  return {
    downloadSelected: () => {
      const urls = rawLinks(true)
      if (urls.length > 0) {
        window.open(urls[0], "_blank")
      }
    },
  }
}
