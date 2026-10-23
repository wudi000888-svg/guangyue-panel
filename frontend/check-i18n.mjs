// Verifies that every Chinese UI string has English copy in src/locales/en.json.
// Chinese source strings are the translation keys, so any Chinese string
// literal (script) or text/attribute (template) must be a catalogue key unless
// it is explicitly allowlisted below. Comments and tests are ignored.
import fs from 'node:fs';
import ts from 'typescript';
import { parse } from '@vue/compiler-sfc';

const en = JSON.parse(fs.readFileSync(new URL('./src/locales/en.json', import.meta.url), 'utf8'));
const cjk = /[㐀-鿿]/u;

// Strings that are never shown through t(): each entry needs a reason.
const allowlist = new Map([
  // Product brand: default panel name, shown verbatim in every language.
  ['广月面板', 'default brand name'],
  // Decorative brand mark on the access-blocked screen (aria-hidden).
  ['月庭 · MOONCOURT', 'brand mark'],
  // Language names are shown in their own language so users can find them.
  ['简体中文', 'language name'],
  ['Language / 语言', 'bilingual language-picker label'],
  // Substrings matched against untranslated server/catalogue text, never displayed.
  ['钱包已变化', 'matched against ApiError.data.error'],
  ['版本来源暂不可用', 'matched against the update service warning'],
]);

const missing = new Set();
const check = (file, value) => {
  if (!cjk.test(value) || en[value] !== undefined || allowlist.has(value)) return;
  // Lines of interpolated template literals are reported as their static parts.
  missing.add(file + ': ' + JSON.stringify(value));
};

function scanScript(file, code) {
  const scanner = ts.createScanner(ts.ScriptTarget.Latest, true, ts.LanguageVariant.Standard, code);
  let token, previous;
  const templateDepth = [];
  while ((token = scanner.scan()) !== ts.SyntaxKind.EndOfFileToken) {
    switch (token) {
      case ts.SyntaxKind.StringLiteral:
      case ts.SyntaxKind.NoSubstitutionTemplateLiteral:
        check(file, scanner.getTokenValue());
        break;
      case ts.SyntaxKind.TemplateHead:
        check(file, scanner.getTokenValue()); templateDepth.push(0);
        break;
      case ts.SyntaxKind.OpenBraceToken:
        if (templateDepth.length) templateDepth[templateDepth.length - 1]++;
        break;
      case ts.SyntaxKind.CloseBraceToken:
        if (templateDepth.length && templateDepth[templateDepth.length - 1] === 0) {
          token = scanner.reScanTemplateToken(false);
          check(file, scanner.getTokenValue());
          if (token === ts.SyntaxKind.TemplateTail) templateDepth.pop();
        } else if (templateDepth.length) templateDepth[templateDepth.length - 1]--;
        break;
      case ts.SyntaxKind.SlashToken:
      case ts.SyntaxKind.SlashEqualsToken:
        // A slash where an expression is expected starts a regular expression.
        if (previous === undefined || ![ts.SyntaxKind.Identifier, ts.SyntaxKind.CloseParenToken, ts.SyntaxKind.CloseBracketToken, ts.SyntaxKind.NumericLiteral, ts.SyntaxKind.StringLiteral].includes(previous)) scanner.reScanSlashToken();
        break;
    }
    previous = token;
  }
}

function scanTemplate(file, node) {
  if (node.type === 2) check(file, node.content.trim().replace(/\s+/g, ' ')); // text
  if (node.type === 5) scanScript(file, node.content.content); // {{ expression }}
  for (const prop of node.props || []) {
    if (prop.type === 6 && prop.value) check(file, prop.value.content); // static attribute
    if (prop.type === 7) { // directive
      if (prop.exp) scanScript(file, prop.exp.content);
      if (prop.arg && prop.arg.isStatic === false) scanScript(file, prop.arg.content);
    }
  }
  for (const child of node.children || []) scanTemplate(file, child);
  if (node.branches) for (const branch of node.branches) scanTemplate(file, branch);
}

for (const file of fs.readdirSync(new URL('./src/', import.meta.url), { recursive: true }).filter(x => /\.(vue|ts)$/.test(x) && !x.endsWith('.test.ts')).sort()) {
  const text = fs.readFileSync(new URL('./src/' + file, import.meta.url), 'utf8');
  if (file.endsWith('.ts')) { scanScript(file, text); continue; }
  const { descriptor, errors } = parse(text, { filename: file });
  if (errors.length) throw new Error(file + ': ' + errors[0].message);
  for (const block of [descriptor.script, descriptor.scriptSetup]) if (block) scanScript(file, block.content);
  if (descriptor.template?.ast) scanTemplate(file, descriptor.template.ast);
}
if (missing.size) throw new Error('Missing English copy (' + missing.size + '):\n' + [...missing].join('\n'));
console.log('English catalogue:', Object.keys(en).length, 'entries; all Chinese UI strings covered');
