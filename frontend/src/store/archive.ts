let archiveExtensions: string[] = [
  ".zip",
  ".tar",
  ".gz",
  ".tgz",
  ".bz2",
  ".tbz2",
  ".xz",
  ".txz",
  ".7z",
  ".rar",
]

export const setArchiveExtensions = (extensions: string[]) => {
  archiveExtensions = extensions
}

export const isArchive = (name: string): boolean => {
  name = name.toLowerCase()
  return archiveExtensions.some((v) => name.endsWith(v))
}
