import test from "node:test"
import assert from "node:assert/strict"
import {
  compareNames,
  compareObjs,
  defaultReverseFor,
  nextSortState,
  sortArrowFor,
} from "./sort.ts"

const sortBy = (items, orderBy, reverse) =>
  [...items].sort((a, b) => compareObjs(a, b, orderBy, reverse)).map((x) => x.name)

test("文件夹在升序和降序下都始终置顶", () => {
  const items = [
    { name: "z-file.txt", size: 500, is_dir: false, created: "2026-03-01T00:00:00Z" },
    { name: "b-dir", size: 100, is_dir: true, created: "2026-01-01T00:00:00Z" },
    { name: "a-file.txt", size: 10, is_dir: false, created: "2026-02-01T00:00:00Z" },
    { name: "a-dir", size: 300, is_dir: true, created: "2026-04-01T00:00:00Z" },
  ]

  assert.deepEqual(sortBy(items, "name", false), ["a-dir", "b-dir", "a-file.txt", "z-file.txt"])
  assert.deepEqual(sortBy(items, "name", true), ["b-dir", "a-dir", "z-file.txt", "a-file.txt"])
  assert.deepEqual(sortBy(items, "created", true), ["a-dir", "b-dir", "z-file.txt", "a-file.txt"])
  assert.deepEqual(sortBy(items, "created", false), ["b-dir", "a-dir", "a-file.txt", "z-file.txt"])
  assert.deepEqual(sortBy(items, "size", true), ["a-dir", "b-dir", "z-file.txt", "a-file.txt"])
})

test("按首字母排序忽略大小写并支持自然数字与中文拼音", () => {
  const items = [
    { name: "Zebra.txt", size: 1, is_dir: false },
    { name: "apple.txt", size: 1, is_dir: false },
    { name: "9.txt", size: 1, is_dir: false },
    { name: "0.txt", size: 1, is_dir: false },
    { name: "file10.mp4", size: 1, is_dir: false },
    { name: "file2.mp4", size: 1, is_dir: false },
    { name: "上海", size: 1, is_dir: false },
    { name: "北京", size: 1, is_dir: false },
    { name: "安徽", size: 1, is_dir: false },
  ]

  const asc = sortBy(items, "name", false)
  assert.ok(asc.indexOf("0.txt") < asc.indexOf("9.txt"))
  assert.ok(asc.indexOf("9.txt") < asc.indexOf("apple.txt"))
  assert.ok(asc.indexOf("apple.txt") < asc.indexOf("Zebra.txt"))
  assert.ok(asc.indexOf("file2.mp4") < asc.indexOf("file10.mp4"))
  assert.ok(asc.indexOf("安徽") < asc.indexOf("北京"))
  assert.ok(asc.indexOf("北京") < asc.indexOf("上海"))
  assert.ok(compareNames("杂物", "Camera") < 0)
  assert.ok(compareNames("Colab Notebooks", "colab_tools") < 0)
})

test("按创建时间排序处理缺失或 Go 零值时间并回退到 modified", () => {
  const items = [
    { name: "zero-ctime", size: 1, is_dir: false, created: "0001-01-01T00:00:00Z", modified: "2026-05-01T00:00:00Z" },
    { name: "old", size: 1, is_dir: false, created: "2025-01-01T00:00:00Z", modified: "2026-06-01T00:00:00Z" },
    { name: "newest", size: 1, is_dir: false, created: "2026-08-01T00:00:00Z", modified: "2026-08-01T00:00:00Z" },
  ]

  assert.deepEqual(sortBy(items, "created", true), ["newest", "zero-ctime", "old"])
  assert.deepEqual(sortBy(items, "created", false), ["old", "zero-ctime", "newest"])
})

test("默认排序方向、切换逻辑与正序↑/倒序↓箭头正确", () => {
  assert.equal(defaultReverseFor("name"), false)
  assert.equal(defaultReverseFor("size"), true)
  assert.equal(defaultReverseFor("created"), true)

  // Time: 新在上(↑) -> 旧在上(↓)
  const t1 = nextSortState({ orderBy: "name", reverse: false }, "created")
  assert.deepEqual(t1, { orderBy: "created", reverse: true })
  assert.equal(sortArrowFor(t1.orderBy, t1.reverse), "↑")

  const t2 = nextSortState(t1, "created")
  assert.deepEqual(t2, { orderBy: "created", reverse: false })
  assert.equal(sortArrowFor(t2.orderBy, t2.reverse), "↓")

  // Size: 大在上(↑) -> 小在上(↓)
  const s1 = nextSortState(t2, "size")
  assert.deepEqual(s1, { orderBy: "size", reverse: true })
  assert.equal(sortArrowFor(s1.orderBy, s1.reverse), "↑")

  const s2 = nextSortState(s1, "size")
  assert.deepEqual(s2, { orderBy: "size", reverse: false })
  assert.equal(sortArrowFor(s2.orderBy, s2.reverse), "↓")

  // Name: a-z/0-9(↑) -> 倒序(↓)
  const n1 = nextSortState(s2, "name")
  assert.deepEqual(n1, { orderBy: "name", reverse: false })
  assert.equal(sortArrowFor(n1.orderBy, n1.reverse), "↑")

  const n2 = nextSortState(n1, "name")
  assert.deepEqual(n2, { orderBy: "name", reverse: true })
  assert.equal(sortArrowFor(n2.orderBy, n2.reverse), "↓")
})
