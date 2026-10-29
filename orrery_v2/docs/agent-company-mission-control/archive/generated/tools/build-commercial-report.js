const fs = require("fs");
const os = require("os");
const path = require("path");
const { spawnSync } = require("child_process");

const rootDir = path.resolve(__dirname, "..");
const buildDir = path.join(rootDir, "_build");
const reportMdPath = path.join(rootDir, "commercial-value-competitor-analysis.md");
const reportHtmlPath = path.join(buildDir, "commercial-value-competitor-analysis.html");
const reportPdfPath = path.join(rootDir, "commercial-value-competitor-analysis.pdf");

const sessionId = "019e73ea-038c-7b30-8853-d42a1aea7181";
const sessionRelPath = path.join(
  ".codex",
  "sessions",
  "2026",
  "05",
  "29",
  "rollout-2026-05-29T21-26-21-019e73ea-038c-7b30-8853-d42a1aea7181.jsonl",
);
const sessionPath = path.join(os.homedir(), sessionRelPath);

const sources = [
  {
    title: "原始会话：商业价值与竞品分析",
    userLine: 572,
    assistantLine: 591,
  },
  {
    title: "原始会话：非研发方向修正",
    userLine: 597,
    assistantLine: 613,
  },
];

function readJsonlLine(lines, lineNumber) {
  const raw = lines[lineNumber - 1];
  if (!raw) {
    throw new Error(`Missing JSONL line ${lineNumber}`);
  }
  return JSON.parse(raw);
}

function messageText(item) {
  const content = item?.payload?.content || [];
  return content
    .map((part) => part.input_text || part.output_text || part.text || "")
    .filter(Boolean)
    .join("\n")
    .trim();
}

function findThreadName() {
  const indexPath = path.join(os.homedir(), ".codex", "session_index.jsonl");
  if (!fs.existsSync(indexPath)) {
    return null;
  }
  const indexLines = fs.readFileSync(indexPath, "utf8").split(/\r?\n/).filter(Boolean);
  for (const line of indexLines) {
    try {
      const record = JSON.parse(line);
      if (record.id === sessionId) {
        return record.thread_name || null;
      }
    } catch {
      // Ignore corrupt index rows; the report should still be buildable.
    }
  }
  return null;
}

function buildMarkdown() {
  if (!fs.existsSync(sessionPath)) {
    throw new Error(`Session file not found: ${sessionPath}`);
  }

  const lines = fs.readFileSync(sessionPath, "utf8").split(/\r?\n/);
  const threadName = findThreadName();
  const generatedAt = new Date().toISOString().slice(0, 10);

  const parts = [
    "# Agent 公司商业价值与竞品分析原始会话整理",
    "",
    `生成日期：${generatedAt}`,
    "",
    "## 会话信息",
    "",
    `- 当前会话 ID：\`${sessionId}\``,
    `- 当前会话名称：${threadName ? threadName : "本地 session_index.jsonl 未记录 thread_name"}`,
    "- 可推断主题：Agent 公司 / Mission Control 流程、商业价值与竞品分析",
    `- 原始会话文件：\`${sessionPath}\``,
    "",
    "## 整理结论",
    "",
    "这两段原始讨论共同确定了产品方向：不要正面做一个更会写代码的 Agent，也不要把市场限制在软件研发。更合适的定位是 Agent Control Plane / Mission Control：控制、编排、审计、协作、验收各种 Agent 和真人协作者。",
    "",
    "软件开发 Agent，例如 Codex、Claude Code、Cursor、Devin、Copilot，应被接入为 Worker 或 runtime adapter，而不是作为正面竞争对象。真正的产品价值在于跨工具、跨真人、跨 Agent 的任务责任、证据、验收、审计和进度投影。",
    "",
    "## 原始片段索引",
    "",
    "| 片段 | 用户行 | Assistant 行 | 主题 |",
    "| --- | ---: | ---: | --- |",
    ...sources.map(
      (source) => `| ${source.title} | ${source.userLine} | ${source.assistantLine} | ${source.title.replace("原始会话：", "")} |`,
    ),
    "",
  ];

  for (const source of sources) {
    const user = messageText(readJsonlLine(lines, source.userLine));
    const assistant = messageText(readJsonlLine(lines, source.assistantLine));
    parts.push(`## ${source.title} (line ${source.assistantLine})`);
    parts.push("");
    parts.push(`来源：\`${sessionPath}:${source.assistantLine}\``);
    parts.push("");
    parts.push("### 用户问题");
    parts.push("");
    parts.push(`> ${user.replace(/\n/g, "\n> ")}`);
    parts.push("");
    parts.push("### Assistant 原始回答");
    parts.push("");
    parts.push(assistant);
    parts.push("");
    parts.push("---");
    parts.push("");
  }

  return parts.join("\n");
}

function escapeHtml(value) {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function inlineMarkdown(text) {
  return escapeHtml(text)
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/\[([^\]]+)\]\((https?:\/\/[^)]+)\)/g, '<a href="$2">$1</a>');
}

function splitTableRow(line) {
  return line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((cell) => cell.trim());
}

function renderMarkdown(markdown) {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  const html = [];
  let paragraph = [];
  let inCode = false;
  let codeLines = [];
  let table = null;
  let listType = null;

  function closeParagraph() {
    if (paragraph.length) {
      html.push(`<p>${inlineMarkdown(paragraph.join(" "))}</p>`);
      paragraph = [];
    }
  }

  function closeList() {
    if (listType) {
      html.push(`</${listType}>`);
      listType = null;
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

  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i].replace(/\s+$/, "");

    if (line.startsWith("```")) {
      closeParagraph();
      closeList();
      closeTable();
      if (inCode) {
        html.push(`<pre><code>${escapeHtml(codeLines.join("\n"))}</code></pre>`);
        inCode = false;
        codeLines = [];
      } else {
        inCode = true;
      }
      continue;
    }

    if (inCode) {
      codeLines.push(line);
      continue;
    }

    if (!line.trim()) {
      closeParagraph();
      closeList();
      closeTable();
      continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.+)$/);
    if (heading) {
      closeParagraph();
      closeList();
      closeTable();
      const level = heading[1].length;
      html.push(`<h${level}>${inlineMarkdown(heading[2])}</h${level}>`);
      continue;
    }

    if (/^\|.+\|$/.test(line) && i + 1 < lines.length && /^\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?$/.test(lines[i + 1])) {
      closeParagraph();
      closeList();
      table = { headers: splitTableRow(line), rows: [] };
      i += 1;
      continue;
    }

    if (table && /^\|.+\|$/.test(line)) {
      table.rows.push(splitTableRow(line));
      continue;
    }

    if (line === "---") {
      closeParagraph();
      closeList();
      closeTable();
      html.push("<hr>");
      continue;
    }

    const quote = line.match(/^>\s?(.*)$/);
    if (quote) {
      closeParagraph();
      closeList();
      closeTable();
      html.push(`<blockquote>${inlineMarkdown(quote[1])}</blockquote>`);
      continue;
    }

    const list = line.match(/^(\s*)([-*]|\d+\.)\s+(.+)$/);
    if (list) {
      closeParagraph();
      closeTable();
      const nextType = /\d+\./.test(list[2]) ? "ol" : "ul";
      if (listType !== nextType) {
        closeList();
        html.push(`<${nextType}>`);
        listType = nextType;
      }
      html.push(`<li>${inlineMarkdown(list[3])}</li>`);
      continue;
    }

    paragraph.push(line.trim());
  }

  if (inCode) {
    html.push(`<pre><code>${escapeHtml(codeLines.join("\n"))}</code></pre>`);
  }
  closeParagraph();
  closeList();
  closeTable();
  return html.join("\n");
}

function buildHtml(markdown) {
  return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>Agent 公司商业价值与竞品分析</title>
<style>
@page { size: A4; margin: 18mm 16mm; }
* { box-sizing: border-box; }
body {
  margin: 0;
  color: #1f2933;
  background: white;
  font-family: "Microsoft YaHei", "Noto Sans CJK SC", "Source Han Sans SC", Arial, sans-serif;
  font-size: 11px;
  line-height: 1.62;
}
h1, h2, h3 { color: #10263a; line-height: 1.32; break-after: avoid; }
h1 { font-size: 26px; border-bottom: 2px solid #8aa0b4; padding-bottom: 8px; }
h2 { font-size: 18px; border-bottom: 1px solid #d7dee8; padding-bottom: 4px; margin-top: 1.4em; }
h3 { font-size: 13px; margin-top: 1.1em; }
p { margin: 0.45em 0; }
a { color: #1f5d7a; }
ul, ol { padding-left: 1.35em; margin: 0.35em 0 0.65em; }
li { margin: 0.2em 0; }
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
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  break-inside: avoid;
}
blockquote {
  border-left: 4px solid #9ab0c0;
  margin: 0.45em 0;
  padding: 0.2em 0 0.2em 1em;
  color: #4b5e6d;
  background: #f7fafc;
}
table { width: 100%; border-collapse: collapse; margin: 0.8em 0 1em; font-size: 9px; }
th, td { border: 1px solid #d7dee8; padding: 5px 6px; vertical-align: top; overflow-wrap: anywhere; }
th { background: #eef3f6; color: #10263a; }
hr { border: 0; border-top: 1px solid #d7dee8; margin: 1.6em 0; }
@media print { body { print-color-adjust: exact; -webkit-print-color-adjust: exact; } }
</style>
</head>
<body>
${renderMarkdown(markdown)}
</body>
</html>`;
}

function findChrome() {
  return [
    process.env.CHROME_PATH,
    "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  ].filter(Boolean).find((candidate) => fs.existsSync(candidate));
}

function renderPdf() {
  const chrome = findChrome();
  if (!chrome) {
    throw new Error("Chrome or Edge was not found; HTML was generated but PDF could not be rendered.");
  }

  const url = `file:///${reportHtmlPath.replace(/\\/g, "/")}`;
  const result = spawnSync(
    chrome,
    [
      "--headless=new",
      "--disable-gpu",
      "--no-sandbox",
      "--allow-file-access-from-files",
      `--print-to-pdf=${reportPdfPath}`,
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
  const markdown = buildMarkdown();
  fs.writeFileSync(reportMdPath, markdown, "utf8");
  fs.writeFileSync(reportHtmlPath, buildHtml(markdown), "utf8");
  renderPdf();
  console.log(`Wrote ${reportMdPath}`);
  console.log(`Wrote ${reportPdfPath}`);
}

main();
