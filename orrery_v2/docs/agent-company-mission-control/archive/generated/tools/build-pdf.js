const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

const rootDir = path.resolve(__dirname, "..");
const buildDir = path.join(rootDir, "_build");
const mergedMdPath = path.join(buildDir, "agent-company-mission-control-combined.md");
const htmlPath = path.join(buildDir, "agent-company-mission-control-combined.html");
const pdfPath = path.join(rootDir, "agent-company-mission-control.pdf");

const documentOrder = [
  "agent-company-mission-control-model.md",
  "technical-architecture-rewrite-plan.md",
  "project-structure-guidelines.md",
  "current-project-gap-analysis.md",
  "implementation-goals/README.md",
  "implementation-goals/backend-build-order.md",
  "implementation-goals/module-decomposition-and-implementation-guide.md",
  "implementation-goals/01-infrastructure-foundation/README.md",
  "implementation-goals/01-infrastructure-foundation/stage-0-goal.md",
  "implementation-goals/01-infrastructure-foundation/stage-0-bdd.md",
  "implementation-goals/01-infrastructure-foundation/stage-0-bdd-coverage.md",
  "implementation-goals/01-infrastructure-foundation/stage-0-implementation-plan.md",
  "implementation-goals/01-infrastructure-foundation/multica-infra-reference.md",
  "implementation-goals/02-core-contracts/README.md",
  "implementation-goals/03-lifecycle-signals/README.md",
  "implementation-goals/04-event-ledger-projection/README.md",
  "implementation-goals/05-resource-registry-memory/README.md",
  "implementation-goals/06-task-delivery-qa/README.md",
  "implementation-goals/07-base-agent-runtime/README.md",
  "implementation-goals/08-organization-staffing/README.md",
  "implementation-goals/09-discussion-inbox/README.md",
  "implementation-goals/10-api-transport/README.md",
  "implementation-goals/99-deferred-frontend-mission-control/README.md",
];

function readUtf8(filePath) {
  return fs.readFileSync(filePath, "utf8").replace(/^\uFEFF/, "");
}

function escapeHtml(value) {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function slugify(text, used) {
  const base =
    text
      .trim()
      .toLowerCase()
      .replace(/[`*_~[\]().,，。:：;；!?！？/\\|]+/g, "")
      .replace(/\s+/g, "-")
      .replace(/^-+|-+$/g, "") || "section";
  let slug = base;
  let i = 2;
  while (used.has(slug)) {
    slug = `${base}-${i}`;
    i += 1;
  }
  used.add(slug);
  return slug;
}

function inlineMarkdown(text) {
  const tokens = [];
  let value = text.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (_, alt, src) => {
    const key = `\u0000${tokens.length}\u0000`;
    tokens.push(`<img alt="${escapeHtml(alt)}" src="${escapeHtml(src)}">`);
    return key;
  });

  value = escapeHtml(value);
  value = value.replace(/`([^`]+)`/g, "<code>$1</code>");
  value = value.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  value = value.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  value = value.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  value = value.replace(/_([^_]+)_/g, "<em>$1</em>");
  value = value.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, label, href) => {
    if (/^(https?:|mailto:|#)/i.test(href)) {
      return `<a href="${escapeHtml(href)}">${label}</a>`;
    }
    return label;
  });
  value = value.replace(/\u0000(\d+)\u0000/g, (_, index) => tokens[Number(index)]);
  return value;
}

function renderBlocks(markdown, sourcePath, state) {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  const html = [];
  let paragraph = [];
  let listStack = [];
  let inCode = false;
  let codeLang = "";
  let codeLines = [];
  let table = null;
  let blockquote = [];

  function closeParagraph() {
    if (paragraph.length) {
      html.push(`<p>${inlineMarkdown(paragraph.join(" "))}</p>`);
      paragraph = [];
    }
  }

  function closeBlockquote() {
    if (blockquote.length) {
      html.push(`<blockquote>${blockquote.map((line) => `<p>${inlineMarkdown(line)}</p>`).join("")}</blockquote>`);
      blockquote = [];
    }
  }

  function closeLists(targetIndent = -1) {
    while (listStack.length && listStack[listStack.length - 1].indent >= targetIndent) {
      const list = listStack.pop();
      html.push(`</li></${list.type}>`);
    }
  }

  function closeTable() {
    if (!table) return;
    const headers = table.headers.map((cell) => `<th>${inlineMarkdown(cell)}</th>`).join("");
    const rows = table.rows
      .map((row) => `<tr>${row.map((cell) => `<td>${inlineMarkdown(cell)}</td>`).join("")}</tr>`)
      .join("");
    html.push(`<table><thead><tr>${headers}</tr></thead><tbody>${rows}</tbody></table>`);
    table = null;
  }

  function closeOpenBlocks() {
    closeParagraph();
    closeBlockquote();
    closeTable();
    closeLists(-1);
  }

  function splitTableRow(line) {
    const trimmed = line.trim().replace(/^\|/, "").replace(/\|$/, "");
    return trimmed.split("|").map((cell) => cell.trim());
  }

  function isTableDivider(line) {
    return /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?\s*$/.test(line);
  }

  function openList(type, indent) {
    html.push(`<${type}>`);
    listStack.push({ type, indent });
  }

  for (let i = 0; i < lines.length; i += 1) {
    const rawLine = lines[i];
    const line = rawLine.replace(/\s+$/, "");

    const fence = line.match(/^```(.*)$/);
    if (fence) {
      closeParagraph();
      closeBlockquote();
      closeTable();
      closeLists(-1);
      if (inCode) {
        html.push(`<pre><code class="language-${escapeHtml(codeLang)}">${escapeHtml(codeLines.join("\n"))}</code></pre>`);
        inCode = false;
        codeLang = "";
        codeLines = [];
      } else {
        inCode = true;
        codeLang = fence[1].trim().split(/\s+/)[0] || "";
      }
      continue;
    }

    if (inCode) {
      codeLines.push(rawLine);
      continue;
    }

    if (!line.trim()) {
      closeParagraph();
      closeBlockquote();
      closeTable();
      closeLists(-1);
      continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.+)$/);
    if (heading) {
      closeOpenBlocks();
      const level = Math.min(6, heading[1].length + 1);
      const title = heading[2].replace(/\s+#*$/, "");
      const id = slugify(title, state.usedIds);
      state.toc.push({ level, title, id });
      html.push(`<h${level} id="${escapeHtml(id)}">${inlineMarkdown(title)}</h${level}>`);
      continue;
    }

    if (/^---+$/.test(line.trim())) {
      closeOpenBlocks();
      html.push("<hr>");
      continue;
    }

    if (line.startsWith(">")) {
      closeParagraph();
      closeTable();
      closeLists(-1);
      blockquote.push(line.replace(/^>\s?/, ""));
      continue;
    }

    if (line.includes("|") && i + 1 < lines.length && isTableDivider(lines[i + 1])) {
      closeParagraph();
      closeBlockquote();
      closeLists(-1);
      table = { headers: splitTableRow(line), rows: [] };
      i += 1;
      continue;
    }

    if (table && line.includes("|")) {
      table.rows.push(splitTableRow(line));
      continue;
    }

    closeTable();

    const listMatch = rawLine.match(/^(\s*)([-*+]|\d+\.)\s+(.+)$/);
    if (listMatch) {
      closeParagraph();
      closeBlockquote();
      const indent = listMatch[1].replace(/\t/g, "    ").length;
      const type = /\d+\./.test(listMatch[2]) ? "ol" : "ul";
      while (listStack.length && indent < listStack[listStack.length - 1].indent) {
        closeLists(listStack[listStack.length - 1].indent);
      }
      if (!listStack.length || indent > listStack[listStack.length - 1].indent || listStack[listStack.length - 1].type !== type) {
        if (listStack.length && listStack[listStack.length - 1].type !== type && indent === listStack[listStack.length - 1].indent) {
          closeLists(indent);
        }
        openList(type, indent);
      } else {
        html.push("</li>");
      }
      html.push(`<li>${inlineMarkdown(listMatch[3])}`);
      continue;
    }

    const imageMatch = line.match(/^!\[([^\]]*)\]\(([^)]+)\)$/);
    if (imageMatch) {
      closeOpenBlocks();
      const alt = imageMatch[1];
      let src = imageMatch[2];
      if (!/^(https?:|data:|file:)/i.test(src)) {
        src = path.resolve(path.dirname(sourcePath), src).replace(/\\/g, "/");
      }
      html.push(`<figure><img alt="${escapeHtml(alt)}" src="${escapeHtml(src)}"><figcaption>${escapeHtml(alt)}</figcaption></figure>`);
      continue;
    }

    paragraph.push(line.trim());
  }

  if (inCode) {
    html.push(`<pre><code class="language-${escapeHtml(codeLang)}">${escapeHtml(codeLines.join("\n"))}</code></pre>`);
  }
  closeOpenBlocks();
  return html.join("\n");
}

function extractTitle(markdown, fallback) {
  const match = markdown.match(/^#\s+(.+)$/m);
  return match ? match[1].trim() : fallback.replace(/\.md$/i, "");
}

function buildDocuments() {
  const docs = [];
  for (const rel of documentOrder) {
    const absolute = path.join(rootDir, rel);
    if (!fs.existsSync(absolute)) {
      throw new Error(`Missing document: ${rel}`);
    }
    const markdown = readUtf8(absolute);
    docs.push({ rel, absolute, title: extractTitle(markdown, rel), markdown });
  }
  return docs;
}

function writeMergedMarkdown(docs) {
  const parts = [
    "# Agent 公司 Mission Control 文档合集",
    "",
    `生成时间：${new Date().toISOString().slice(0, 10)}`,
    "",
    "## 目录",
    "",
    ...docs.map((doc, index) => `${index + 1}. ${doc.title} (${doc.rel.replaceAll("\\", "/")})`),
    "",
    "---",
    "",
  ];

  for (const [index, doc] of docs.entries()) {
    const body = doc.markdown.replace(/^\s*#\s+.+$/m, "").trim();
    parts.push(`# 第 ${index + 1} 章：${doc.title}`);
    parts.push("");
    parts.push(`来源：\`${doc.rel.replaceAll("\\", "/")}\``);
    parts.push("");
    parts.push(body);
    parts.push("");
    parts.push("---");
    parts.push("");
  }

  fs.writeFileSync(mergedMdPath, parts.join("\n"), "utf8");
}

function buildToc(toc) {
  return toc
    .filter((item) => item.level <= 3)
    .map((item) => {
      const className = `toc-level-${item.level}`;
      return `<a class="${className}" href="#${escapeHtml(item.id)}">${escapeHtml(item.title)}</a>`;
    })
    .join("\n");
}

function writeHtml(docs) {
  const state = { usedIds: new Set(), toc: [] };
  const sections = docs
    .map((doc, index) => {
      const chapterId = slugify(`chapter-${index + 1}-${doc.title}`, state.usedIds);
      state.toc.push({ level: 1, title: `第 ${index + 1} 章：${doc.title}`, id: chapterId });
      const body = doc.markdown.replace(/^\s*#\s+.+$/m, "").trim();
      return [
        `<section class="chapter">`,
        `<h1 id="${escapeHtml(chapterId)}">第 ${index + 1} 章：${inlineMarkdown(doc.title)}</h1>`,
        `<p class="source">来源：${escapeHtml(doc.rel.replaceAll("\\", "/"))}</p>`,
        renderBlocks(body, doc.absolute, state),
        `</section>`,
      ].join("\n");
    })
    .join("\n");

  const html = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>Agent 公司 Mission Control 文档合集</title>
<style>
@page {
  size: A4;
  margin: 18mm 16mm;
}

* {
  box-sizing: border-box;
}

body {
  margin: 0;
  color: #1f2933;
  background: #ffffff;
  font-family: "Microsoft YaHei", "Noto Sans CJK SC", "Source Han Sans SC", Arial, sans-serif;
  font-size: 11px;
  line-height: 1.62;
}

.cover {
  min-height: 940px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  border-bottom: 1px solid #d7dee8;
}

.cover .kicker {
  color: #3d5a6c;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0;
  margin-bottom: 18px;
}

.cover h1 {
  color: #0f1f2e;
  font-size: 40px;
  line-height: 1.18;
  margin: 0 0 24px;
  page-break-after: auto;
}

.cover p {
  max-width: 620px;
  color: #4e5f6d;
  font-size: 14px;
}

.meta {
  margin-top: 42px;
  padding-top: 18px;
  border-top: 1px solid #d7dee8;
  color: #526371;
}

.toc {
  break-after: page;
  page-break-after: always;
}

.toc h1 {
  font-size: 24px;
  margin-top: 0;
}

.toc a {
  display: block;
  color: #183449;
  text-decoration: none;
  border-bottom: 1px solid #edf1f5;
  padding: 4px 0;
}

.toc-level-1 {
  font-weight: 700;
  margin-top: 8px;
}

.toc-level-2 {
  padding-left: 18px !important;
}

.toc-level-3 {
  padding-left: 36px !important;
  color: #526371 !important;
}

.chapter {
  break-before: page;
  page-break-before: always;
}

h1, h2, h3, h4, h5, h6 {
  color: #10263a;
  line-height: 1.32;
  margin: 1.2em 0 0.45em;
  break-after: avoid;
  page-break-after: avoid;
}

h1 {
  font-size: 24px;
  margin-top: 0;
  border-bottom: 2px solid #8aa0b4;
  padding-bottom: 8px;
}

h2 {
  font-size: 18px;
  border-bottom: 1px solid #d7dee8;
  padding-bottom: 4px;
}

h3 {
  font-size: 14px;
}

h4, h5, h6 {
  font-size: 12px;
}

p {
  margin: 0.45em 0;
}

.source {
  color: #697986;
  font-size: 10px;
  margin: 0 0 18px;
}

a {
  color: #1f5d7a;
}

ul, ol {
  padding-left: 1.35em;
  margin: 0.35em 0 0.65em;
}

li {
  margin: 0.2em 0;
}

code {
  font-family: "Cascadia Mono", Consolas, "Courier New", monospace;
  background: #f3f6f8;
  color: #243646;
  border-radius: 3px;
  padding: 0.08em 0.28em;
  font-size: 0.92em;
}

pre {
  background: #f4f7fa;
  border: 1px solid #d7dee8;
  border-radius: 6px;
  padding: 10px 12px;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  break-inside: avoid;
  page-break-inside: avoid;
}

pre code {
  background: transparent;
  padding: 0;
  border-radius: 0;
  font-size: 9px;
  line-height: 1.45;
}

blockquote {
  border-left: 4px solid #9ab0c0;
  margin: 0.8em 0;
  padding: 0.1em 0 0.1em 1em;
  color: #4b5e6d;
  background: #f7fafc;
}

table {
  width: 100%;
  border-collapse: collapse;
  margin: 0.8em 0 1em;
  font-size: 9px;
  break-inside: avoid;
}

th, td {
  border: 1px solid #d7dee8;
  padding: 5px 6px;
  vertical-align: top;
  overflow-wrap: anywhere;
}

th {
  background: #eef3f6;
  color: #10263a;
}

hr {
  border: 0;
  border-top: 1px solid #d7dee8;
  margin: 1.6em 0;
}

img {
  max-width: 100%;
  height: auto;
}

figure {
  margin: 1em 0;
}

figcaption {
  color: #697986;
  font-size: 10px;
  text-align: center;
}

@media print {
  body {
    print-color-adjust: exact;
    -webkit-print-color-adjust: exact;
  }
}
</style>
</head>
<body>
<section class="cover">
  <div class="kicker">Orrery v2 / Agent Company Mission Control</div>
  <h1>Agent 公司 Mission Control 文档合集</h1>
  <p>本 PDF 由 <code>orrery_v2/docs/agent-company-mission-control</code> 下的 Markdown 文档合并生成，保留原始章节内容，并按总览、架构、结构规范、差异分析、后端实施 goals 的顺序整理。</p>
  <div class="meta">
    <p>生成日期：${new Date().toISOString().slice(0, 10)}</p>
    <p>文档数量：${docs.length}</p>
  </div>
</section>
<section class="toc">
  <h1>目录</h1>
  ${buildToc(state.toc)}
</section>
${sections}
</body>
</html>`;

  fs.writeFileSync(htmlPath, html, "utf8");
}

function findChrome() {
  const candidates = [
    process.env.CHROME_PATH,
    "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  ].filter(Boolean);
  return candidates.find((candidate) => fs.existsSync(candidate));
}

function renderPdf() {
  const chrome = findChrome();
  if (!chrome) {
    throw new Error("Chrome or Edge was not found; HTML was generated but PDF could not be rendered.");
  }

  const url = `file:///${htmlPath.replace(/\\/g, "/")}`;
  const result = spawnSync(
    chrome,
    [
      "--headless=new",
      "--disable-gpu",
      "--no-sandbox",
      "--allow-file-access-from-files",
      `--print-to-pdf=${pdfPath}`,
      "--print-to-pdf-no-header",
      url,
    ],
    { encoding: "utf8" },
  );

  if (result.status !== 0) {
    throw new Error(`Chrome failed with code ${result.status}\n${result.stderr || result.stdout}`);
  }
}

function main() {
  fs.mkdirSync(buildDir, { recursive: true });
  const docs = buildDocuments();
  writeMergedMarkdown(docs);
  writeHtml(docs);
  renderPdf();

  const pdfSize = fs.statSync(pdfPath).size;
  const htmlSize = fs.statSync(htmlPath).size;
  console.log(JSON.stringify({ pdfPath, htmlPath, mergedMdPath, documents: docs.length, pdfSize, htmlSize }, null, 2));
}

main();
