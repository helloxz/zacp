/**
 * CodeMirror 6 语言推断：按文件扩展名 / 特殊文件名返回对应语言扩展。
 *
 * 供工作区文件编辑器（FileEditorDrawer）与智能体配置编辑器（AgentConfigEditorModal）
 * 共用，避免两处各自维护一份语言映射。
 * 语言包选择：官方 @codemirror/lang-* 优先，legacy-modes 兜底。
 */
import type { Extension } from '@codemirror/state'
import {
  Language,
  LanguageSupport,
  StreamLanguage,
} from '@codemirror/language'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { python } from '@codemirror/lang-python'
import { go } from '@codemirror/lang-go'
import { markdown } from '@codemirror/lang-markdown'
import { html } from '@codemirror/lang-html'
import { css } from '@codemirror/lang-css'
import { xml } from '@codemirror/lang-xml'
import { sql } from '@codemirror/lang-sql'
import { yaml } from '@codemirror/lang-yaml'
import { rust } from '@codemirror/lang-rust'
import { cpp } from '@codemirror/lang-cpp'
import { java } from '@codemirror/lang-java'
import { php } from '@codemirror/lang-php'
import { sass } from '@codemirror/lang-sass'
import { less } from '@codemirror/lang-less'
import { dockerFile } from '@codemirror/legacy-modes/mode/dockerfile'
import { nginx } from '@codemirror/legacy-modes/mode/nginx'
import { powerShell } from '@codemirror/legacy-modes/mode/powershell'
import { properties } from '@codemirror/legacy-modes/mode/properties'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import { toml } from '@codemirror/legacy-modes/mode/toml'

/** 按扩展名索引（小写，不含点）；Language = 纯语法高亮，LanguageSupport = 带配套扩展 */
const EXT_LANGS: Record<string, Language | LanguageSupport> = {
  // JS/TS 系列
  js: javascript(),
  jsx: javascript({ jsx: true }),
  mjs: javascript(),
  cjs: javascript(),
  ts: javascript({ typescript: true }),
  tsx: javascript({ jsx: true, typescript: true }),
  json: json(),
  jsonc: json(),
  // 常用后端/脚本语言
  py: python(),
  pyw: python(),
  go: go(),
  rs: rust(),
  c: cpp(),
  h: cpp(),
  cpp: cpp(),
  hpp: cpp(),
  cc: cpp(),
  cxx: cpp(),
  java: java(),
  php: php(),
  // 标记 / 样式
  md: markdown(),
  markdown: markdown(),
  mdx: markdown(),
  html: html(),
  htm: html(),
  vue: html(),
  css: css(),
  // sass({indented:false}) 才是 scss 花括号语法；默认 indented:true 是 .sass 缩进语法
  scss: sass({ indented: false }),
  sass: sass({ indented: true }),
  less: less(),
  xml: xml(),
  svg: xml(),
  // 配置 / 数据
  sql: sql(),
  yml: yaml(),
  yaml: yaml(),
  toml: StreamLanguage.define(toml),
  ini: StreamLanguage.define(properties),
  conf: StreamLanguage.define(properties),
  properties: StreamLanguage.define(properties),
  env: StreamLanguage.define(properties),
  // 脚本 / 运维
  sh: StreamLanguage.define(shell),
  bash: StreamLanguage.define(shell),
  zsh: StreamLanguage.define(shell),
  ps1: StreamLanguage.define(powerShell),
  nginx: StreamLanguage.define(nginx),
}

/** 无扩展名的特殊文件名（Dockerfile 等） */
const SPECIAL_NAMES: Record<string, Language | LanguageSupport> = {
  dockerfile: StreamLanguage.define(dockerFile),
}

/** 按文件路径推断 CM6 语言；无法识别时返回 null（编辑器按纯文本处理） */
export function detectLanguage(path: string): Extension | null {
  const name = (path.split('/').pop() ?? '').toLowerCase()
  const special = SPECIAL_NAMES[name]
  if (special) return special
  const idx = name.lastIndexOf('.')
  if (idx < 0) return null
  return EXT_LANGS[name.slice(idx + 1)] ?? null
}
