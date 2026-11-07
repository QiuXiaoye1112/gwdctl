import {
  NavigateOptions,
  SetParams,
  useLocation,
  useNavigate,
  useParams,
  _mergeSearchString,
} from "@solidjs/router"
import { createMemo, untrack } from "solid-js"
import { encodePath, joinBase, log, pathDir, pathJoin, trimBase } from "~/utils"
import { clearHistory } from "~/store"

const useRouter = () => {
  const publicPath = (path: string) => path.replace(/^\/@s(?=\/|$)/, "/share")
  const navigate = useNavigate()
  const location = useLocation()
  const params = useParams()
  const pathname = createMemo(() => {
    const path = trimBase(decodeURIComponent(location.pathname))
    return path.replace(/^\/share(?=\/|$)/, "/@s")
  })
  const isShare = createMemo(() => {
    return pathname().startsWith("/@s")
  })
  return {
    to: (
      path: string,
      ignore_root?: boolean,
      options?: Partial<NavigateOptions>,
    ) => {
      path = publicPath(path)
      if (!ignore_root && path.startsWith("/")) {
        path = joinBase(path)
      }
      log("to:", path)
      clearHistory(decodeURIComponent(path))
      navigate(path, options)
    },
    replace: (to: string) => {
      const path = joinBase(encodePath(publicPath(pathJoin(pathDir(pathname()), to)), true))
      clearHistory(decodeURIComponent(path))
      navigate(path)
    },
    pushHref: (to: string): string => {
      return encodePath(publicPath(pathJoin(pathname(), to)))
    },
    back: () => {
      navigate(-1)
    },
    forward: () => {
      navigate(1)
    },
    pathname: pathname,
    isShare: isShare,
    search: location.search,
    searchParams: location.query,
    setSearchParams: (
      params: SetParams,
      options?: Partial<NavigateOptions>,
    ) => {
      const searchString = untrack(() =>
        _mergeSearchString(location.search, params),
      )
      navigate(location.pathname + searchString + location.hash, {
        scroll: false,
        ...options,
        resolve: true,
      })
    },
    params: params,
  }
}

export { useRouter }
