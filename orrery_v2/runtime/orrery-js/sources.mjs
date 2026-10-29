// Copyright 2026 Minvia. SPDX-License-Identifier: Apache-2.0
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { sha256 } from './bundles.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const manifestPath = 'compliance/orrery-js-sources.json';
const read = name => readFileSync(path.join(root, name), 'utf8').replaceAll('\r\n', '\n');

export function ownPackage(project, spec) {
  const manifest = JSON.parse(read(manifestPath));
  const directory = path.relative(root, path.resolve(project, spec.slice('file:'.length))).replaceAll('\\', '/');
  const record = manifest.packages.find(item => item.directory === directory);
  if (!record) throw new Error(`Unregistered Orrery package: ${spec}`);
  const pkg = JSON.parse(read(directory + '/package.json'));
  for (const key of ['name', 'version', 'license']) if (pkg[key] !== record[key]) throw new Error('Orrery package identity differs');
  if (!pkg.private || pkg.license !== 'Apache-2.0') throw new Error('Invalid Orrery publication setting');
  const expected = [...pkg.files, 'package.json'].map(name => directory + '/' + name).sort();
  if (JSON.stringify(expected) !== JSON.stringify(Object.keys(record.files).sort())) throw new Error('Orrery file set differs');
  for (const [name, checksum] of Object.entries(record.files)) {
    if (sha256(read(name)) !== checksum) throw new Error(`Stale Orrery package source: ${name}`);
  }
  return { name: pkg.name, version: pkg.version, license: pkg.license,
    localSource: { manifest: manifestPath, manifestSha256: sha256(read(manifestPath)), directory, files: record.files },
    notice: read(directory + '/NOTICE'), licenseText: read(directory + '/LICENSE') };
}
