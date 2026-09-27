#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createRequire, isBuiltin } from 'node:module';
import { fileURLToPath } from 'node:url';

const EXPECTED_TYPESCRIPT_VERSION = '6.0.3';

function inside(root, absolute) {
  const relative = path.relative(root, absolute);
  return relative !== '' && relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}

function cleanEntry(entry) {
  return typeof entry === 'string' && entry.length > 0 && !entry.includes('\\') && !entry.includes('\0')
    && !path.posix.isAbsolute(entry) && path.posix.normalize(entry) === entry
    && entry !== '.' && entry !== '..' && !entry.startsWith('../');
}

function literalText(ts, node) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text;
  return null;
}

function loadTypeScript(compilerRoot) {
  const packagePath = path.join(compilerRoot, 'package.json');
  const requireFromCompiler = createRequire(packagePath);
  const compiler = requireFromCompiler('typescript');
  if (compiler.version !== EXPECTED_TYPESCRIPT_VERSION) {
    throw new Error(`unexpected TypeScript version: ${compiler.version || 'unknown'}`);
  }
  return compiler;
}

function analyze({ root, compilerRoot, entries }) {
  const files = new Set();
  let rootPath;
  let ts;
  try {
    rootPath = fs.realpathSync(path.resolve(root));
    if (!fs.statSync(rootPath).isDirectory()) throw new Error('candidate root is not a directory');
    ts = loadTypeScript(path.resolve(compilerRoot));
  } catch (error) {
    return { needs_npm: true, files: [], reason: `cannot load trusted parser or candidate root: ${error.message}` };
  }

  const visiting = new Set();
  let failure = null;

  function reject(reason) {
    if (!failure) failure = reason;
  }

  function absoluteForEntry(entry, fromFile) {
    let absolute;
    if (fromFile) {
      if (!entry.startsWith('./') && !entry.startsWith('../')) {
        reject(`unknown module specifier: ${entry}`);
        return null;
      }
      if (entry.includes('\\') || entry.includes('\0') || entry.includes('?') || entry.includes('#')) {
        reject(`unsupported local module specifier: ${entry}`);
        return null;
      }
      if (!/\.(?:mjs|js)$/.test(entry)) {
        reject(`local module must name an explicit .mjs or .js file: ${entry}`);
        return null;
      }
      absolute = path.resolve(path.dirname(fromFile), entry);
    } else {
      if (!cleanEntry(entry)) {
        reject(`entry must be a clean repository-relative path: ${String(entry)}`);
        return null;
      }
      absolute = path.resolve(rootPath, ...entry.split('/'));
    }

    if (!inside(rootPath, absolute)) {
      reject(`module escapes candidate root: ${entry}`);
      return null;
    }
    let cursor = rootPath;
    for (const segment of path.relative(rootPath, absolute).split(path.sep)) {
      cursor = path.join(cursor, segment);
      try {
        const stat = fs.lstatSync(cursor);
        if (stat.isSymbolicLink()) {
          reject(`module traverses a symbolic link: ${path.relative(rootPath, absolute)}`);
          return null;
        }
      } catch (error) {
        reject(`module is missing or unreadable: ${path.relative(rootPath, absolute)} (${error.code || error.message})`);
        return null;
      }
    }
    try {
      if (!fs.statSync(absolute).isFile()) {
        reject(`module is not a regular file: ${path.relative(rootPath, absolute)}`);
        return null;
      }
    } catch (error) {
      reject(`module is missing or unreadable: ${path.relative(rootPath, absolute)} (${error.code || error.message})`);
      return null;
    }
    return absolute;
  }

  function visitSpecifier(specifier, fromFile) {
    if (failure) return;
    if (isBuiltin(specifier)) {
      const builtinName = specifier.startsWith('node:') ? specifier.slice('node:'.length) : specifier;
      if (builtinName === 'vm' || builtinName.startsWith('vm/')) reject(`node:vm is not allowed: ${specifier}`);
      return;
    }
    if (!specifier.startsWith('./') && !specifier.startsWith('../')) {
      reject(`external or unknown module requires npm: ${specifier}`);
      return;
    }
    const absolute = absoluteForEntry(specifier, fromFile);
    if (absolute) visitFile(absolute);
  }

  function visitFile(absolute) {
    if (failure || files.has(absolute) || visiting.has(absolute)) return;
    visiting.add(absolute);
    let sourceText;
    try {
      sourceText = fs.readFileSync(absolute, 'utf8');
    } catch (error) {
      reject(`cannot read ${path.relative(rootPath, absolute)}: ${error.message}`);
      visiting.delete(absolute);
      return;
    }
    const source = ts.createSourceFile(absolute, sourceText, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
    if (source.parseDiagnostics?.length) {
      const diagnostic = source.parseDiagnostics[0];
      const detail = ts.flattenDiagnosticMessageText(diagnostic.messageText, ' ');
      reject(`JavaScript syntax cannot be parsed in ${path.relative(rootPath, absolute)}: ${detail}`);
      visiting.delete(absolute);
      return;
    }

    files.add(absolute);
    function visit(node) {
      if (failure) return;

      if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) {
        if (node.moduleSpecifier) {
          const specifier = literalText(ts, node.moduleSpecifier);
          if (specifier === null) reject(`nonliteral import/export in ${path.relative(rootPath, absolute)}`);
          else visitSpecifier(specifier, absolute);
        }
      }
      if (ts.isImportEqualsDeclaration(node)) {
        reject(`import-equals syntax is not supported in ${path.relative(rootPath, absolute)}`);
      }

      if (ts.isCallExpression(node)) {
        if (node.expression.kind === ts.SyntaxKind.ImportKeyword) {
          const specifier = node.arguments.length === 1 ? literalText(ts, node.arguments[0]) : null;
          if (specifier === null) reject(`nonliteral dynamic import in ${path.relative(rootPath, absolute)}`);
          else visitSpecifier(specifier, absolute);
        } else if (ts.isIdentifier(node.expression) && node.expression.text === 'require') {
          const specifier = node.arguments.length === 1 ? literalText(ts, node.arguments[0]) : null;
          if (specifier === null) reject(`nonliteral require in ${path.relative(rootPath, absolute)}`);
          else visitSpecifier(specifier, absolute);
        } else if (ts.isIdentifier(node.expression) && node.expression.text === 'eval') {
          reject(`eval is not allowed in ${path.relative(rootPath, absolute)}`);
        }
      }

      if (ts.isNewExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'Function') {
        reject(`Function constructor is not allowed in ${path.relative(rootPath, absolute)}`);
      }
      if (ts.isIdentifier(node)) {
        if (node.text === 'createRequire') reject(`createRequire is not allowed in ${path.relative(rootPath, absolute)}`);
        if (node.text === 'Function') reject(`Function usage is not allowed in ${path.relative(rootPath, absolute)}`);
        if (node.text === 'eval' && !(ts.isPropertyAccessExpression(node.parent) && node.parent.name === node)) {
          reject(`eval is not allowed in ${path.relative(rootPath, absolute)}`);
        }
        if (node.text === 'require' && !(ts.isCallExpression(node.parent) && node.parent.expression === node)) {
          reject(`indirect require is not allowed in ${path.relative(rootPath, absolute)}`);
        }
      }
      if (ts.isPropertyAccessExpression(node)) {
        if (node.name.text === 'require' || node.name.text === '_load' || node.name.text === 'eval') {
          reject(`indirect module loading is not allowed in ${path.relative(rootPath, absolute)}`);
        }
        if (node.name.text === 'resolve' && ts.isMetaProperty(node.expression) && node.expression.keywordToken === ts.SyntaxKind.ImportKeyword) {
          reject(`import.meta.resolve is not allowed in ${path.relative(rootPath, absolute)}`);
        }
      }
      if (ts.isElementAccessExpression(node)) {
        const member = literalText(ts, node.argumentExpression);
        if (['require', 'createRequire', '_load', 'eval', 'Function'].includes(member)
          || (ts.isIdentifier(node.expression) && ['globalThis', 'module'].includes(node.expression.text))) {
          reject(`computed module loading or evaluation is not allowed in ${path.relative(rootPath, absolute)}`);
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(source);
    visiting.delete(absolute);
  }

  if (!Array.isArray(entries) || entries.length === 0) {
    reject('entries must be a non-empty array');
  } else {
    for (const entry of entries) {
      if (failure) break;
      const absolute = absoluteForEntry(entry, null);
      if (absolute) visitFile(absolute);
    }
  }

  return {
    needs_npm: Boolean(failure),
    files: [...files].map((file) => path.relative(rootPath, file).split(path.sep).join('/')).sort(),
    reason: failure || 'only Node built-ins and explicit local JavaScript imports',
  };
}

export function analyzeBrowserDependencies(options) {
  try {
    return analyze(options);
  } catch (error) {
    return { needs_npm: true, files: [], reason: `dependency analysis failed closed: ${error.message}` };
  }
}

function parseArguments(argv) {
  const values = new Map();
  for (let index = 0; index < argv.length; index += 1) {
    const key = argv[index];
    if (!['--root', '--compiler-root', '--entries-json'].includes(key) || index + 1 >= argv.length || values.has(key)) {
      throw new Error('usage: node scripts/ci/browser_npm_dependencies.mjs --root DIRECTORY --compiler-root DIRECTORY --entries-json JSON_ARRAY');
    }
    values.set(key, argv[++index]);
  }
  if (values.size !== 3) throw new Error('missing required analyzer arguments');
  const entries = JSON.parse(values.get('--entries-json'));
  if (!Array.isArray(entries)) throw new Error('--entries-json must be a JSON array');
  return { root: values.get('--root'), compilerRoot: values.get('--compiler-root'), entries };
}

const invokedPath = process.argv[1] && path.resolve(process.argv[1]);
if (invokedPath === fileURLToPath(import.meta.url)) {
  let result;
  try {
    result = analyze(parseArguments(process.argv.slice(2)));
  } catch (error) {
    result = { needs_npm: true, files: [], reason: `analyzer input error: ${error.message}` };
  }
  process.stdout.write(`${JSON.stringify(result)}\n`);
  if (result.needs_npm && result.reason.startsWith('analyzer input error:')) process.exitCode = 2;
}
