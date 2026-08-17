/**
 * 可文本编辑文件判定：按扩展名白名单判断一个文件是否允许在文本编辑器中打开/保存。
 *
 * 与后端 service/files.go 的 editableTextExts / isEditableTextName 保持一致
 * （两端各自维护，前端控制 UI 入口，后端是最终防线——即使此处漏放行，
 * 后端 Read/Write 接口仍会拒绝）。
 *
 * 规则：
 * 1. 隐藏文件（以 `.` 开头，如 .gitignore、.env、.npmrc）→ 放行，
 *    代码仓库高频文本配置；极端情况（如 .DS_Store）由后端内容检测兜底拒绝；
 * 2. 无扩展名（不含 `.`，如 README、LICENSE、Makefile、Dockerfile、Cargo.lock）→ 放行；
 * 3. 扩展名（最后一个点之后的小写部分）命中白名单 → 放行；
 * 4. 其余（mp3/mp4/zip/exe 等）→ 不可编辑。
 */

/** 可编辑扩展名白名单（小写，不含点）；与后端 editableTextExts 保持一致 */
const EDITABLE_TEXT_EXTS = new Set([
  // JS/TS 系列
  'js', 'jsx', 'mjs', 'cjs', 'ts', 'tsx',
  // Java 系 / Android / Kotlin
  'java', 'kt', 'kts',
  // C/C++ 系
  'c', 'h', 'cpp', 'hpp', 'cc', 'cxx', 'm', 'mm',
  // 其他后端/脚本语言
  'py', 'pyw', 'go', 'rs', 'php', 'cs', 'csx', 'swift', 'rb', 'lua', 'dart',
  'scala', 'pl', 'pm', 'r', 'jl', 'ex', 'exs', 'erl', 'hrl', 'hs', 'lhs',
  // 标记 / 样式
  'md', 'markdown', 'mdx', 'rst', 'adoc',
  'html', 'htm', 'vue', 'svelte',
  'css', 'scss', 'sass', 'less', 'styl',
  // 配置 / 数据
  'xml', 'svg', 'sql', 'yml', 'yaml', 'toml',
  'ini', 'conf', 'cfg', 'config', 'properties', 'env',
  'json', 'jsonc', 'lock',
  'proto', 'graphql', 'gql',
  // 脚本 / 运维
  'sh', 'bash', 'zsh', 'ksh', 'fish', 'ps1', 'psm1', 'bat', 'cmd', 'nginx',
  // 通用文本 / 数据
  'txt', 'text', 'log', 'csv', 'tsv', 'patch', 'diff',
])

/** 文件名是否为可文本编辑（对非目录文件调用） */
export function isEditableFileName(name: string): boolean {
  const base = name.split('/').pop() ?? ''
  // 隐藏文件（点开头）：视为常见文本配置放行
  if (base.startsWith('.')) return true
  // 无扩展名：README / LICENSE / Dockerfile / Makefile 等
  const dot = base.lastIndexOf('.')
  if (dot < 0) return true
  return EDITABLE_TEXT_EXTS.has(base.slice(dot + 1).toLowerCase())
}