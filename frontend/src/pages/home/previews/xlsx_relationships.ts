import JSZip from "jszip"

const workbookRelations = "xl/_rels/workbook.xml.rels"
const relationsNS =
  "http://schemas.openxmlformats.org/package/2006/relationships"
const worksheetTypes = new Set([
  "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet",
  "http://purl.oclc.org/ooxml/officeDocument/relationships/worksheet",
])

// The bundled ExcelJS build does not resolve package-absolute worksheet paths.
export async function normalizeWorksheetTargets(
  source: ArrayBuffer,
): Promise<ArrayBuffer> {
  const zip = await JSZip.loadAsync(source)
  const entry = zip.file(workbookRelations)
  if (!entry) return source

  const xml = new DOMParser().parseFromString(
    await entry.async("text"),
    "application/xml",
  )
  if (
    xml.documentElement.namespaceURI !== relationsNS ||
    xml.documentElement.localName !== "Relationships" ||
    xml.getElementsByTagName("parsererror").length > 0
  ) {
    return source
  }

  let changed = false
  for (const relationship of Array.from(
    xml.documentElement.getElementsByTagNameNS(relationsNS, "Relationship"),
  )) {
    if (
      !worksheetTypes.has(relationship.getAttribute("Type") ?? "") ||
      relationship.getAttribute("TargetMode") === "External"
    ) {
      continue
    }
    const target = relationship.getAttribute("Target") ?? ""
    if (!target.startsWith("/xl/worksheets/")) continue
    const entryPath = target.slice(1)
    if (!zip.file(entryPath)) continue
    relationship.setAttribute("Target", entryPath.slice("xl/".length))
    changed = true
  }
  if (!changed) return source

  // Only the in-memory copy is adapted; users download the unmodified file.
  zip.file(workbookRelations, new XMLSerializer().serializeToString(xml))
  return zip.generateAsync({ type: "arraybuffer", compression: "DEFLATE" })
}
