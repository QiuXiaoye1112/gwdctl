import { useSelectedLink } from "~/hooks"

const triggerDownload = (url: string) => {
  const a = document.createElement("a")
  a.href = url
  a.download = ""
  a.style.display = "none"
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
}

export const useDownload = () => {
  const { rawLinks } = useSelectedLink()
  return {
    downloadSelected: () => {
      const urls = rawLinks(true)
      urls.forEach((url, index) => {
        if (index === 0) {
          triggerDownload(url)
        } else {
          setTimeout(() => {
            triggerDownload(url)
          }, index * 250)
        }
      })
    },
  }
}
