const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

const rootDir = path.resolve(__dirname, "..");
const buildDir = path.join(rootDir, "_build");
const mdPath = path.join(rootDir, "agent-company-business-plan.md");
const htmlPath = path.join(buildDir, "agent-company-business-plan.html");
const pdfPath = path.join(rootDir, "agent-company-business-plan.pdf");

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
    if (!paragraph.length) return;
    html.push(`<p>${inlineMarkdown(paragraph.join(" "))}</p>`);
    paragraph = [];
  }

  function closeList() {
    if (!listType) return;
    html.push(`</${listType}>`);
    listType = null;
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
<title>Orrery 人机共生组织控制平台商业企划书</title>
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
h1 { font-size: 25px; border-bottom: 2px solid #8aa0b4; padding-bottom: 8px; }
h2 { font-size: 17px; border-bottom: 1px solid #d7dee8; padding-bottom: 4px; margin-top: 1.35em; }
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
table { width: 100%; border-collapse: collapse; margin: 0.8em 0 1em; font-size: 9px; }
th, td { border: 1px solid #d7dee8; padding: 5px 6px; vertical-align: top; overflow-wrap: anywhere; }
th { background: #eef3f6; color: #10263a; }
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
  const markdown = fs.readFileSync(mdPath, "utf8");
  fs.writeFileSync(htmlPath, buildHtml(markdown), "utf8");
  renderPdf();
  console.log(JSON.stringify({ htmlPath, pdfPath, pdfSize: fs.statSync(pdfPath).size }, null, 2));
}

main();
