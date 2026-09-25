import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

export const shards = ['core', 'research', 'payment', 'process'];
export function shardFor(pkg) {
  const prefix = 'github.com/vitlane/vitlane/server/';
  if (!pkg.startsWith(prefix)) throw new Error(`Unexpected Go package: ${pkg}`);
  const path = pkg.slice(prefix.length);
  if (path.startsWith('internal/curation/research/')) return 'research';
  if (path.startsWith('internal/ordering/payment/')) return 'payment';
  if (path.startsWith('internal/ordering/process/')) return 'process';
  return 'core';
}
export function partition(packages) {
  if (!packages.length || new Set(packages).size !== packages.length) throw new Error('Empty or duplicate Go package inventory');
  const groups = Object.fromEntries(shards.map(s => [s, []]));
  for (const pkg of packages) groups[shardFor(pkg)].push(pkg);
  for (const shard of shards) if (!groups[shard].length) throw new Error(`Empty shard: ${shard}`);
  return groups;
}
if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const shard = process.argv[2];
  if (!shards.includes(shard)) throw new Error('Unknown server shard');
  const list = spawnSync('go', ['list', './...'], { encoding: 'utf8' });
  if (list.status !== 0) throw new Error(`go list failed: ${list.stderr}`);
  console.log(partition(list.stdout.trim().split('\n'))[shard].join('\n'));
}
